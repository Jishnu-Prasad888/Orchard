// Package likeutil escapes SQL LIKE wildcards in user-supplied path segments
// used to build prefix filters, and exposes the shared ESCAPE clause string.
//
// A folder literally named "a_b" or "100%" must never widen a child/member
// query to siblings; escaping makes the wildcards literal.
package likeutil

import "strings"

// Escape returns s with LIKE wildcards (% _ \) made literal for use on the
// right-hand side of a LIKE comparison paired with EscapeClause.
func Escape(s string) string {
	return strings.NewReplacer(
		`\`, `\\`,
		`%`, `\%`,
		`_`, `\_`,
	).Replace(s)
}

// EscapeClause is the SQL tail that declares the escape character used by
// Escape. Append it to every LIKE/NOT LIKE comparison whose argument went
// through Escape.
const EscapeClause = "ESCAPE '\\'"
