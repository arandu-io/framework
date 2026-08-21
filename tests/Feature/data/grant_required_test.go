package feature

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/arandu-io/framework/tests"
)

// TestRepositoryWithoutGrantDoesNotCompile proves the claim in the only way it
// can be proven.
//
// The claim of this framework is not "remember to authorize". It is that the
// path from a handler to the database cannot be written without a Grant. A claim
// about compilation can only be proven by attempting a compilation, so this test
// runs the toolchain over fixtures under testdata and requires them to fail --
// with the specific message, because a fixture that fails for an unrelated
// reason proves nothing.
//
// The fixtures stay under data/testdata and did not travel here with the test.
// The toolchain skips any directory named testdata when it expands a pattern,
// and that is the only thing keeping code written to fail out of the build; a
// fixture anywhere else would be compiled by ./... and break it.
func TestRepositoryWithoutGrantDoesNotCompile(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a fixture with the go tool")
	}

	root := tests.ModuleRoot(t)

	cases := []struct {
		fixture string
		reason  string
		want    string
	}{
		{
			fixture: "./data/testdata/missing_grant",
			reason:  "calling a repository without a Grant",
			want:    "not enough arguments in call to repo.Find",
		},
		{
			fixture: "./data/testdata/forged_grant",
			reason:  "building a valid Grant outside the security package",
			want:    "cannot refer to unexported field valid",
		},
	}

	for _, c := range cases {
		t.Run(c.reason, func(t *testing.T) {
			// From the module root, so the fixture path reads the way it
			// would be typed by hand and the toolchain resolves it against
			// this module rather than against wherever this test now lives.
			cmd := exec.Command("go", "vet", c.fixture)
			cmd.Dir = root

			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("%s compiled. The framework thesis is broken.\n%s", c.reason, out)
			}
			if !strings.Contains(string(out), c.want) {
				t.Fatalf("the fixture failed for the wrong reason.\nwant a message containing: %s\ngot:\n%s", c.want, out)
			}
		})
	}
}
