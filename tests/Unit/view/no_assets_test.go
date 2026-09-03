package unit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/arandu-io/framework/tests"
)

// TestNoAssetFileLivesInThisModule fails the moment a browser asset reappears
// under view/assets/ here.
//
// This module has no go:embed directive. A file dropped into that directory is
// compiled into nothing and served to nobody, so it cannot be checked by
// anything that runs: it is read only by a person, who has no way of telling it
// apart from the file a browser actually receives. That already cost a team
// half a day -- they opened the copy that lived here, did not find the client
// behaviour it was missing, and reported that the behaviour did not exist.
//
// The reappearance is not malice, it is a sync: the two directories used to
// hold the same names, so copying one over the other looks like housekeeping.
// This test is what turns that copy into a failed build instead of a second
// source of truth, and its message is the address of the first one.
func TestNoAssetFileLivesInThisModule(t *testing.T) {
	dir := filepath.Join(tests.ModuleRoot(t), "view", "assets")

	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		// The directory is gone, which is the state this test asks for. Git
		// does not track an empty directory, so this is what a clean checkout
		// looks like.
		return
	}
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}

	for _, e := range entries {
		t.Errorf("%s is back, and nothing in this module embeds or serves it. "+
			"The assets a browser receives are embedded by github.com/arandu-io/hesape/view, "+
			"in its view/assets/ directory, and its view/THIRD_PARTY.md is the copyright notice "+
			"beside them: change the file there, and delete this one",
			filepath.Join("view", "assets", e.Name()))
	}
}
