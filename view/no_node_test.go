package view_test

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoNodeAnywhere is RULE 13, checked rather than promised.
//
// A project runs with `git clone && aru dev`. No node_modules, no package.json,
// no JS lockfile, and no Node installed. In Laravel, Node entered through the
// error page -- Illuminate/Foundation/resources/exceptions/renderer/ carries a
// package.json and a vite.config.js. Ours is html/template, inline.
//
// It is here and not with the rest of what used to be assets_test.go because it
// is not a test of this package: it walks THIS REPOSITORY, and the repository
// still has a tree of its own after the view runtime moved to hesape. The
// identical guard in hesape/view walks hesape, which is a different tree.
func TestNoNodeAnywhere(t *testing.T) {
	forbidden := []string{"package.json", "package-lock.json", "yarn.lock",
		"pnpm-lock.yaml", "bun.lockb", "node_modules", "vite.config.js", "vite.config.ts"}

	// ".." and not ".": the whole repository, because the promise is about the
	// repository. The reference projects cloned next to it are full of
	// package.json and are read-only material rather than code that ships
	// (RULE 7), which is why the walk starts here and not one level higher.
	root := ".."
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if strings.Contains(path, "/.git/") {
			return nil
		}
		for _, name := range forbidden {
			if d.Name() == name {
				t.Errorf("%s exists: the promise is that a project runs without Node", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
