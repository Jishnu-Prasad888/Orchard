package fts

import (
	"strings"
	"testing"
)

func TestBuildExpressionBasicTerms(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"   ", ""},
		{"beach", `"beach"*`},
		{"beach holidays", `("beach"* AND "holidays"*)`},
		{"holiday/beach", `("holiday"* AND "beach"*)`},
		{"2019-01", `"2019-01"*`},
	}
	for _, c := range cases {
		got, err := BuildExpression(c.in)
		if err != nil {
			t.Errorf("BuildExpression(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("BuildExpression(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBuildExpressionOperators(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"a AND b", `("a"* AND "b"*)`},
		{"a OR b", `("a"* OR "b"*)`},
		{"a or b", `("a"* OR "b"*)`},
		{"a -b", `("a"* NOT "b"*)`},
		{"a AND NOT b", `("a"* NOT "b"*)`},
		{"a -b -c", `(("a"* NOT "b"*) NOT "c"*)`},
		{"a -b c", `(("a"* NOT "b"*) AND "c"*)`},
		{"a -b OR c", `(("a"* NOT "b"*) OR "c"*)`},
		{"a AND b OR c", `(("a"* AND "b"*) OR "c"*)`},
		{"(a OR b) AND c", `((("a"* OR "b"*)) AND "c"*)`},
		{"(a -b)", `(("a"* NOT "b"*))`},
	}
	for _, c := range cases {
		got, err := BuildExpression(c.in)
		if err != nil {
			t.Errorf("BuildExpression(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("BuildExpression(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBuildExpressionPhrase(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{`"moon landing"`, `"moon" "landing"`},
		{`beach "moon landing"`, `("beach"* AND "moon" "landing")`},
		{`"say ""hi"" now"`, `"say" "hi" "now"`},
	}
	for _, c := range cases {
		got, err := BuildExpression(c.in)
		if err != nil {
			t.Errorf("BuildExpression(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("BuildExpression(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBuildExpressionRejectsMalformed(t *testing.T) {
	cases := []string{
		`"unterminated`,
		`("unbalanced`,
		`a)`,
		`AND`,
		`a AND`,
		`AND a`,
		`a OR OR b`,
		`NOT`,
		`NOT a`,
		`-a`,
		`-a b`,
		`a OR -b`,
		"a b) c",
		`-`,
		`*`,
		`a*extra`,
		`"run "tail"`,
		`beach ) holiday`,
		`(((a OR b)`,
	}
	for _, c := range cases {
		if _, err := BuildExpression(c); err != ErrInvalid {
			t.Errorf("BuildExpression(%q) = err %v, want ErrInvalid", c, err)
		}
	}
}

func TestBuildExpressionHandlesUnicodeAndNoise(t *testing.T) {
	got, err := BuildExpression("Öster-Malmö/beach! 2023")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, wantPart := range []string{`"Öster-Malmö"*`, `"beach"*`, `"2023"*`} {
		if !strings.Contains(got, wantPart) {
			t.Errorf("expected %q inside %q", wantPart, got)
		}
	}
}

func TestBuildExpressionClampsInput(t *testing.T) {
	if _, err := BuildExpression(strings.Repeat("a", MaxQueryBytes+1)); err != ErrInvalid {
		t.Error("expected ErrInvalid for oversized input")
	}
	// A long but bounded pile of terms is rejected too.
	query := strings.Repeat("w ", MaxTerms+5)
	if _, err := BuildExpression(query); err != ErrInvalid {
		t.Error("expected ErrInvalid for too many terms")
	}
}

func TestBuildExpressionPrefixTerm(t *testing.T) {
	got, err := BuildExpression("beach*")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != `"beach"*` {
		t.Errorf("got %q, want %q", got, `"beach"*`)
	}
}
