package foundation_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// deletedDirectories are paths an Arandu project no longer has.
//
// cmd/app/ was the wiring directory before ADR 0019 moved the tree to Laravel's
// shape: the binary is main.go at the root and the composition is
// bootstrap/app.go. Nothing lives under cmd/ anymore.
var deletedDirectories = []string{"cmd/app"}

// TestNoDocCommentPointsAtADeletedDirectory guards the instructions that reach
// pkg.go.dev.
//
// A doc comment is published documentation the moment a tag is pushed, and
// "register it in cmd/app/main.go" sends the reader to a directory `aru new`
// never creates. That is worse than no instruction: it reads as authoritative
// and it costs a search to disprove.
//
// Test files are exempt on purpose. They are not published, and a test comment
// that says "the wiring moved from cmd/app/main.go to bootstrap/app.go" is a
// record of why the message reads the way it does.
func TestNoDocCommentPointsAtADeletedDirectory(t *testing.T) {
	// ".." and not ".": the promise is about every symbol the module exports,
	// not about this package.
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(body), "\n") {
			for _, gone := range deletedDirectories {
				if strings.Contains(line, gone) {
					t.Errorf("%s:%d names %s, a directory ADR 0019 removed: the wiring is bootstrap/app.go",
						path, i+1, gone)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
