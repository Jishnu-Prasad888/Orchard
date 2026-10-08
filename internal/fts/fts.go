// Package fts turns free-text user queries into safe FTS5 MATCH expressions.
//
// FTS5 has a rich query language of its own: boolean operators, phrase
// searches, NEAR, column qualifiers, and bm25() weighting. Passing user text
// straight into MATCH is a footgun — malformed expressions turn into 500s, and
// heavy syntax can put surprising load on the query planner. This package
// whitelists a small, predictable subset of the syntax and quotes every
// literal, so any input either becomes a valid expression or is rejected with
// ErrInvalid.
package fts

import (
	"errors"
	"strings"
	"unicode"
)

// ErrInvalid is returned for queries that cannot be expressed safely.
var ErrInvalid = errors.New("invalid search expression")

const (
	// MaxQueryBytes caps the raw input a client may submit.
	MaxQueryBytes = 1024
	// MaxTerms caps how many search terms one query may produce.
	MaxTerms = 64
	// MaxPhraseTerms caps the number of words inside a single phrase.
	MaxPhraseTerms = 32
)

// BuildExpression parses raw and returns a safe FTS5 MATCH expression.
//
// Supported syntax:
//
//	beach          plain term; prefix-matched like "beachday.jpg"
//	beach*         explicit prefix term
//	"moon landing" exact phrase
//	-beach         excluded term (equivalent: NOT beach)
//	a AND b        both required (implicit AND is the default between
//	               adjacent terms)
//	a OR b         either term; AND binds tighter than OR
//	(a OR b) AND c grouped with parentheses
//
// Operators, quotes, and parentheses must be balanced; dangling operators and
// unbalanced delimiters are rejected with ErrInvalid. Empty or whitespace-only
// input returns ("", nil).
func BuildExpression(raw string) (string, error) {
	if len(raw) > MaxQueryBytes {
		return "", ErrInvalid
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}

	tokens, err := tokenize(raw)
	if err != nil {
		return "", err
	}
	return parse(tokens)
}

// token represents one lexical element of a query.
type token struct {
	kind kind
	text string
}

type kind int

const (
	kindTerm  kind = iota // a quoted term or phrase, ready for MATCH
	kindAnd               // AND
	kindOr                // OR
	kindNot               // unary negation marker
	kindOpen              // (
	kindClose             // )
)

// tokenize scans raw into a stream of tokens, quoting every term and phrase.
func tokenize(raw string) ([]token, error) {
	var out []token
	termCount := 0
	runes := []rune(raw)

	push := func(t token) error {
		if t.kind == kindTerm {
			termCount++
			if termCount > MaxTerms {
				return ErrInvalid
			}
		}
		out = append(out, t)
		return nil
	}

	i := 0
	for i < len(runes) {
		c := runes[i]

		if unicode.IsSpace(c) {
			i++
			continue
		}

		switch c {
		case '(':
			if err := push(token{kind: kindOpen}); err != nil {
				return nil, err
			}
			i++
			continue
		case ')':
			if err := push(token{kind: kindClose}); err != nil {
				return nil, err
			}
			i++
			continue
		case '"':
			phrase, next, err := readPhrase(runes, i)
			if err != nil {
				return nil, err
			}
			if err := push(token{kind: kindTerm, text: phrase}); err != nil {
				return nil, err
			}
			i = next
			continue
		}

		// A term (possibly negated) or an explicit boolean operator.
		chunkEnd := i
		for chunkEnd < len(runes) {
			r := runes[chunkEnd]
			if unicode.IsSpace(r) || r == '(' || r == ')' || r == '"' {
				break
			}
			chunkEnd++
		}
		chunk := string(runes[i:chunkEnd])
		i = chunkEnd

		negated := false
		if strings.HasPrefix(chunk, "-") {
			if len(chunk) == 1 {
				return nil, ErrInvalid
			}
			negated = true
			chunk = chunk[1:]
		}

		if isOperatorWord(chunk) {
			if negated {
				return nil, ErrInvalid
			}
			op := token{kind: kindAnd}
			switch strings.ToUpper(chunk) {
			case "AND":
				op.kind = kindAnd
			case "OR":
				op.kind = kindOr
			case "NOT":
				op.kind = kindNot
			}
			if err := push(op); err != nil {
				return nil, err
			}
			continue
		}

		parts, err := splitTermChunk(chunk)
		if err != nil {
			return nil, err
		}
		if len(parts) == 0 {
			continue
		}
		if negated {
			if err := push(token{kind: kindNot}); err != nil {
				return nil, err
			}
		}
		for _, p := range parts {
			if err := push(token{kind: kindTerm, text: p}); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// readPhrase reads a double-quoted phrase starting at the opening quote in
// runes[start] and returns the FTS5-escaped phrase plus the index just past the
// closing quote. A doubled double-quote ("" inside the phrase) escapes a literal
// quote.
func readPhrase(runes []rune, start int) (string, int, error) {
	var sb strings.Builder
	i := start + 1
	closed := false
	for i < len(runes) {
		if runes[i] == '"' {
			if i+1 < len(runes) && runes[i+1] == '"' {
				sb.WriteRune('"')
				i += 2
				continue
			}
			closed = true
			break
		}
		sb.WriteRune(runes[i])
		i++
	}
	if !closed {
		return "", 0, ErrInvalid
	}

	words := strings.Fields(sb.String())
	if len(words) == 0 {
		return "", 0, ErrInvalid
	}
	if len(words) > MaxPhraseTerms {
		return "", 0, ErrInvalid
	}
	quoted := make([]string, len(words))
	for k, w := range words {
		w = strings.Trim(w, `"`)
		if w == "" {
			return "", 0, ErrInvalid
		}
		quoted[k] = `"` + w + `"`
	}
	return strings.Join(quoted, " "), i + 1, nil
}

// splitTermChunk breaks a raw term into one or more safe quoted literals,
// dropping characters that are meaningless to full-text search and turning path
// separators into separate terms. A trailing '*' requests prefix matching on
// the final literal.
func splitTermChunk(chunk string) ([]string, error) {
	runes := []rune(chunk)
	var literals []string
	var sb strings.Builder

	flush := func() {
		if sb.Len() > 0 {
			literals = append(literals, sb.String())
			sb.Reset()
		}
	}

	for i, r := range runes {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-':
			sb.WriteRune(r)
		case r == '*':
			if i != len(runes)-1 {
				// '*' is only meaningful as the final character.
				return nil, ErrInvalid
			}
			flush()
			if len(literals) == 0 {
				return nil, ErrInvalid
			}
			literals[len(literals)-1] += "*"
		default:
			// Any other rune separates terms (path separator, punctuation,
			// column syntax, control characters...).
			flush()
		}
	}
	flush()

	// Plain terms are prefix-matched by default (so "beach" finds
	// "beachday.jpg" without the user typing a glob). An explicit trailing '*'
	// from the chunk already carried the marker forward.
	for i, l := range literals {
		if strings.HasSuffix(l, "*") {
			literals[i] = `"` + strings.TrimSuffix(l, "*") + `"*`
		} else {
			literals[i] = `"` + l + `"*`
		}
	}
	return literals, nil
}

func isOperatorWord(s string) bool {
	switch strings.ToUpper(s) {
	case "AND", "OR", "NOT":
		return true
	}
	return false
}

// parse validates token ordering (operators need operands, parentheses match)
// and rebuilds a canonical FTS5 expression. Implicit AND between adjacent
// operands is preserved and made explicit for predictability.
//
// Negations are rendered as binary NOT, which is what this SQLite build's FTS5
// accepts (`-term` and standalone unary NOT go unanswered by the tokenizer, so
// "everything except X" queries are rejected).
func parse(tokens []token) (string, error) {
	if len(tokens) == 0 {
		return "", nil
	}
	p := &parser{tokens: tokens}
	expr, err := p.orExpr()
	if err != nil {
		return "", err
	}
	if p.i != len(p.tokens) {
		return "", ErrInvalid
	}
	return expr, nil
}

type parser struct {
	tokens []token
	i      int
}

// orExpr parses one or more and-exprs joined by OR.
func (p *parser) orExpr() (string, error) {
	left, err := p.andExpr()
	if err != nil {
		return "", err
	}
	if p.peekKind(kindOr) {
		p.i++
		right, err := p.orExpr()
		if err != nil {
			return "", err
		}
		return "(" + left + " OR " + right + ")", nil
	}
	return left, nil
}

// andExpr parses one or more operands joined by AND (explicit or implicit
// adjacency). A NOT operand at any point continues the exclusion chain of the
// expression built so far: "a -b" and "a AND NOT b" both render as
// "(a NOT b)".
func (p *parser) andExpr() (string, error) {
	left, err := p.unary()
	if err != nil {
		return "", err
	}
	for {
		switch {
		case p.peekKind(kindAnd):
			p.i++
			if p.peekKind(kindNot) {
				p.i++
				right, err := p.unary()
				if err != nil {
					return "", err
				}
				left = "(" + left + " NOT " + right + ")"
				continue
			}
		case p.peekKind(kindNot):
			p.i++
			right, err := p.unary()
			if err != nil {
				return "", err
			}
			left = "(" + left + " NOT " + right + ")"
			continue
		case !p.atOperandStart():
			return left, nil
		}
		right, err := p.unary()
		if err != nil {
			return "", err
		}
		left = "(" + left + " AND " + right + ")"
	}
}

// unary parses a parenthesised expression or a bare term. A negation token
// cannot open an expression ("everything except X" is inexpressible), so a
// leading NOT resolves to ErrInvalid.
func (p *parser) unary() (string, error) { return p.primary() }

// primary parses a parenthesised expression or a bare term.
func (p *parser) primary() (string, error) {
	if p.i >= len(p.tokens) {
		return "", ErrInvalid
	}
	t := p.tokens[p.i]
	switch t.kind {
	case kindOpen:
		p.i++
		inner, err := p.orExpr()
		if err != nil {
			return "", err
		}
		if !p.peekKind(kindClose) {
			return "", ErrInvalid
		}
		p.i++
		return "(" + inner + ")", nil
	case kindTerm:
		p.i++
		return t.text, nil
	default:
		return "", ErrInvalid
	}
}

// peekKind reports whether the next token has the given kind.
func (p *parser) peekKind(k kind) bool {
	return p.i < len(p.tokens) && p.tokens[p.i].kind == k
}

// atOperandStart reports whether the parse cursor begins a new operand,
// meaning an implicit AND can be inferred.
func (p *parser) atOperandStart() bool {
	if p.i >= len(p.tokens) {
		return false
	}
	switch p.tokens[p.i].kind {
	case kindTerm, kindOpen:
		return true
	}
	return false
}
