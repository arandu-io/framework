package unit

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arandu-io/framework/tests"
)

// TestNoNodeAnywhere checks the no-Node promise rather than stating it.
//
// A project runs with `git clone && aru dev`. No node_modules, no package.json,
// no JS lockfile, and no Node installed. The error page is html/template inline,
// so no asset build enters through it either.
//
// It asks the question of THIS REPOSITORY, which still has a tree of its own
// after the view runtime moved out of it. The identical guard beside that
// runtime walks the module it moved to, which is a different tree, and neither
// answers for the other.
func TestNoNodeAnywhere(t *testing.T) {
	forbidden := []string{"package.json", "package-lock.json", "yarn.lock",
		"pnpm-lock.yaml", "bun.lockb", "node_modules", "vite.config.js", "vite.config.ts"}

	// The whole repository, because the promise is about the repository. The
	// projects cloned next to it are full of package.json and are read-only
	// material rather than code that ships, which is why the walk starts at the
	// module and not one level higher.
	root := tests.ModuleRoot(t)
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
