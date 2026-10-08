package likeutil

import "testing"

func TestEscape(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", "plain"},
		{"a_b", "a\\_b"},
		{"100%", "100\\%"},
		{"back\\slash", "back\\\\slash"},
		{"a/b", "a/b"}, // path separators are not wildcards
	}
	for _, tc := range cases {
		if got := Escape(tc.in); got != tc.want {
			t.Errorf("Escape(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
