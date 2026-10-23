package conformance

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// failingKit is the environment variable a child process reads to know which
// broken application it runs a kit against.
const failingKit = "ARANDU_CONFORMANCE_FAILING_KIT"

// broken is every kit run against an application that breaks what it checks,
// filled by the test files beside this one, with the words the failure has to
// carry.
var broken = map[string]brokenCase{}

type brokenCase struct {
	run  func(t *testing.T)
	want []string
}

// TestTheBrokenApplicationRunsInAChild is the body the child process runs.
// Outside one it does nothing.
func TestTheBrokenApplicationRunsInAChild(t *testing.T) {
	name := os.Getenv(failingKit)
	if name == "" {
		t.Skip("runs only as the child of TestEveryKitFailsOnTheApplicationThatBreaksIt")
	}
	c, ok := broken[name]
	if !ok {
		t.Fatalf("no broken case %q", name)
	}
	c.run(t)
}

// TestEveryKitFailsOnTheApplicationThatBreaksIt: a kit that never fails proves
// nothing. A kit takes a *testing.T and fails it, which the test that owns the
// T cannot observe, so each case runs in a child process that has to exit
// non-zero with the failure message.
func TestEveryKitFailsOnTheApplicationThatBreaksIt(t *testing.T) {
	if os.Getenv(failingKit) != "" {
		t.Skip("the child runs one case, not the table")
	}
	for name, c := range broken {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cmd := exec.Command(os.Args[0], "-test.run=^TestTheBrokenApplicationRunsInAChild$", "-test.count=1", "-test.v")
			cmd.Env = append(os.Environ(), failingKit+"="+name)
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("the kit passed against an application that breaks it:\n%s", out)
			}
			if !strings.Contains(string(out), "--- FAIL") {
				t.Fatalf("the child failed without a test failure:\n%s", out)
			}
			for _, want := range c.want {
				if !strings.Contains(string(out), want) {
					t.Fatalf("the child failed without the message %q:\n%s", want, out)
				}
			}
		})
	}
}

// writeTree writes files under a fresh directory, paths in slash form.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
