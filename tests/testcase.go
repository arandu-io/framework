// Package tests is the base the suites of this module build on.
//
// What belongs here is what more than one suite needs. A helper one suite uses
// belongs beside that suite, in the category directory it lives in.
//
// The suites:
//
//	tests/Unit/     one thing, with nothing running
//	tests/Feature/  a whole behaviour, across the layers that produce it
//	tests/E2E/      the sequence of requests a client actually makes
//
// The categories are directories rather than file suffixes because the
// toolchain only runs a file whose name ends in _test.go, and a naming scheme
// that competes with that rule switches the suite off without failing.
package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// modulePath is the module the root answers for. It is written out rather than
// read from anywhere, because reading it from the file being searched for would
// make any go.mod the answer.
const modulePath = "github.com/arandu-io/framework"

// ModuleRoot answers the directory holding this module's go.mod, and fails the
// test if it cannot be found.
//
// Several tests read the source tree itself -- for a class that is never
// compiled into a stylesheet, for a file a project must not have, for a doc
// comment naming a directory that is gone. Each of those walks a root, and each
// one is silent when the root is wrong: a walk over an empty directory finds no
// violation and reports success.
//
// So the root is asked for rather than written down. A relative path is
// correct only from the depth it was written at, and a test that moves one
// level keeps passing while checking nothing.
func ModuleRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("looking for the module root: %v", err)
	}

	for {
		body, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil {
			for _, line := range strings.Split(string(body), "\n") {
				if strings.TrimSpace(line) == "module "+modulePath {
					return dir
				}
			}
			t.Fatalf("%s holds a go.mod, and it does not declare %s", dir, modulePath)
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod declaring %s above the working directory", modulePath)
		}
		dir = parent
	}
}
