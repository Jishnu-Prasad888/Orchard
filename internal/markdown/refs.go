// Package markdown contains helpers for Cairn's memory Markdown format.
//
// The format is plain Markdown with the addition of internal reference links
// written as [[type:id]] or [[type:id|label]]. These links make it possible
// for a memory to reference photos, videos, files, other memories, albums,
// people, and tags without requiring the user to navigate to those resources
// manually.
//
// Reference types are intentionally constrained to a fixed set so the
// renderer and picker can offer predictable behaviour. Unknown or malformed
// links are treated as ordinary text.
package markdown

import (
	"regexp"
)

// RefType identifies the kind of resource an internal reference points to.
type RefType string

// The supported internal reference types. The names are part of the public
// Markdown contract and are used verbatim inside [[type:id]] links.
const (
	RefMedia  RefType = "media"  // [[media:<file-id>]] — photo, video, or file
	RefMemory RefType = "memory" // [[memory:<memory-id>]]
	RefAlbum  RefType = "album"  // [[album:<album-id>]]
	RefPerson RefType = "person" // [[person:<person-id>]]
	RefTag    RefType = "tag"    // [[tag:<tag-id>]]
)

// Valid reports whether t is one of the supported reference types.
func (t RefType) Valid() bool {
	switch t {
	case RefMedia, RefMemory, RefAlbum, RefPerson, RefTag:
		return true
	}
	return false
}

// Reference is a single parsed internal link found in a memory body.
type Reference struct {
	// Type is the link target kind (media, memory, album, person, tag).
	Type RefType
	// ID is the first colon-or-pipe separated segment after the type, i.e.
	// the identifier of the referenced resource.
	ID string
	// Label is the optional display label given after [[type:id|label]].
	Label string
	// Raw is the exact matched [[...]] text, used for link rewriting.
	Raw string
	// Start and End are byte offsets of the link within the source body,
	// so callers can splice replacements back into the original text.
	Start int
	End   int
}

// refPattern matches [[type:id]] and [[type:id|label]] links. The type must be
// lowercase alphabetic, the id any non-pipe, non-bracket text, and the label
// (optional) any text up to the closing brackets. The pattern never spans
// newlines.
var refPattern = regexp.MustCompile(`\[\[([a-z]+):([^|\]\n]+)(?:\|([^\]\n]*))?\]\]`)

// ParseReferences scans body and returns every well-formed internal
// reference it contains, in document order. Duplicate links are kept; callers
// that need uniqueness can deduplicate on (Type, ID).
func ParseReferences(body string) []Reference {
	matches := refPattern.FindAllStringSubmatchIndex(body, -1)
	if len(matches) == 0 {
		return nil
	}
	refs := make([]Reference, 0, len(matches))
	for _, m := range matches {
		raw := body[m[0]:m[1]]
		ref := Reference{
			Type:  RefType(body[m[2]:m[3]]),
			ID:    body[m[4]:m[5]],
			Raw:   raw,
			Start: m[0],
			End:   m[1],
		}
		if m[6] >= 0 {
			ref.Label = body[m[6]:m[7]]
		}
		if !ref.Type.Valid() || ref.ID == "" {
			continue
		}
		refs = append(refs, ref)
	}
	return refs
}
