package foundation

import "testing"

// TestCSRFCoversReadsTheExemptionAsTheCheckDoes writes down which route
// patterns an exemption reaches: a subtree reaches what lies under it, an
// exact path its own length, a parameter any segment, and an open pattern --
// a trailing slash or a remainder -- every length past its fixed segments.
func TestCSRFCoversReadsTheExemptionAsTheCheckDoes(t *testing.T) {
	for _, tc := range []struct {
		exemption, pattern string
		want               bool
	}{
		{"/hooks/", "/hooks/acme", true},
		{"/hooks/", "/hooks", false},
		{"/hooks/", "/hooks/{$}", true},
		{"/hooks/", "/{tenant}/x", true},
		{"/hooks/", "/notes/{id}", false},
		{"/hooks/", "/hooks/{rest...}", true},
		{"/hooks/", "/", true},
		{"/hooks/", "/notes/", false},
		{"/hooks/acme", "/hooks/acme", true},
		{"/hooks/acme", "/hooks/acme/x", false},
		{"/hooks/acme", "/hooks/{name}", true},
		{"/hooks/{name}", "/hooks/acme/rotate", false},
		{"/hooks/{name}/rotate", "/hooks/acme/rotate", true},
		{"/hooks/acme", "/", true},
		{"/hooks/acme", "/{$}", false},
		{"/hooks/acme", "/hooks/{rest...}", true},
		{"/hooks/acme", "/files/{rest...}", false},
		{"/hooks", "/hooks/{rest...}", false},
	} {
		if got := csrfCovers(tc.exemption, tc.pattern); got != tc.want {
			t.Errorf("csrfCovers(%q, %q) = %v, want %v", tc.exemption, tc.pattern, got, tc.want)
		}
	}
}
