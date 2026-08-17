package foundation_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNothingWeakensTheTLSDefaults guards a default this module gets for free
// and can lose in one line.
//
// Nothing here terminates TLS: the single listen site is ListenAndServe, so
// whatever negotiates with a browser sits in front of the process. What the
// defaults do cover is every outbound client the module grows, and they hold
// only while nobody writes the field that replaces them.
//
// The three names below are the three ways to lose them silently:
//
//   - CurvePreferences, because Go's default already includes the hybrid
//     post-quantum key exchange and writing the field replaces the whole list.
//     The reason somebody writes it is interoperability with one old server, and
//     the cost lands on every connection.
//   - the tlsmlkem GODEBUG, which is the second spelling of that same loss and
//     the one the Go release notes hand to whoever hits the interoperability
//     problem. A guard that catches only the field catches the less likely half.
//   - InsecureSkipVerify, which leaves a handshake that verifies no certificate.
//
// It walks the repository rather than this package, the way the no-Node guard
// does, because the promise is about the repository and not about one package.
// It lives here because the listen site does, and it names the file it is
// written in so the three names above do not fail their own check.
func TestNothingWeakensTheTLSDefaults(t *testing.T) {
	forbidden := []struct{ name, loses string }{
		{"CurvePreferences", "writing it replaces the default list, which is where the hybrid post-quantum key exchange comes from"},
		{"tlsmlkem", "the GODEBUG drops the same key exchange without a tls.Config to read it from"},
		{"InsecureSkipVerify", "it leaves a handshake that verifies no certificate"},
	}

	// ".." and not ".": the whole repository. The projects cloned beside it are
	// read-only material rather than code that ships, which is why the walk
	// starts here and not one level higher.
	root := ".."
	self := "tls_test.go"

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.Contains(path, "/.git/") || d.Name() == self {
			return nil
		}
		// Go source and the module file: one is where a tls.Config is built, the
		// other is where a godebug directive would sit.
		if filepath.Ext(path) != ".go" && d.Name() != "go.mod" {
			return nil
		}

		body, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for _, f := range forbidden {
			if strings.Contains(string(body), f.name) {
				t.Errorf("%s names %s: %s", path, f.name, f.loses)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
