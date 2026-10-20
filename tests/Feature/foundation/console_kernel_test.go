package feature

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/arandu-io/framework/foundation"
	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/hesape/console"
)

// run answers what a command line printed on each stream and the status it
// exited with.
func run(t *testing.T, app *foundation.Application, args ...string) (stdout, stderr string, status int) {
	t.Helper()
	var out, errOut bytes.Buffer
	status = app.WithConsole(&out, &errOut, strings.NewReader("")).HandleCommand(context.Background(), args)
	return out.String(), errOut.String(), status
}

// commander is a module that declares a console command and records whether it
// was booted before the command ran.
type commander struct {
	name    string
	command string
	booted  bool
	ranBoot bool
}

func (m *commander) Name() string               { return m.name }
func (m *commander) Routes(*fhttp.Router)       {}
func (m *commander) Boot(context.Context) error { m.booted = true; return nil }
func (m *commander) Commands() []console.Command {
	return []console.Command{{
		Name:        m.command,
		Description: "a module's own command",
		Run: func(_ context.Context, o *console.IO) error {
			m.ranBoot = m.booted
			o.Line("module command ran")
			return nil
		},
	}}
}

// handComposed is an Application made by New and composed by hand, the way an
// application that has not moved to Configure is.
func handComposed(t *testing.T, mods ...foundation.Module) *foundation.Application {
	t.Helper()
	return foundation.New(settingsFor(t, "APP_NAME", "billing").Framework).Register(mods...)
}

// TestAHandComposedApplicationAnswersWhatNeedsNothingElse: HandleCommand works
// on an Application made by New, with the commands that need nothing but the
// Application -- and a Commandable module's beside them.
func TestAHandComposedApplicationAnswersWhatNeedsNothingElse(t *testing.T) {
	stdout, _, status := run(t, handComposed(t), "help")
	if status != 0 {
		t.Fatalf("help exited %d", status)
	}
	for _, name := range []string{"serve", "routes", "version", "vendor:publish"} {
		if !strings.Contains(stdout, "  "+name+" ") {
			t.Errorf("the listing does not name %s:\n%s", name, stdout)
		}
	}
	if strings.Contains(stdout, "migrate") {
		t.Errorf("a hand-composed application listed a command it has no database for:\n%s", stdout)
	}

	stdout, _, status = run(t, handComposed(t), "version")
	if status != 0 || strings.TrimSpace(stdout) != "billing dev (unknown)" {
		t.Errorf("version = %q, status %d", stdout, status)
	}

	stdout, stderr, status := run(t, handComposed(t), "routes")
	if status != 0 || !strings.Contains(stdout, "/_arandu/health") {
		t.Errorf("routes = %q (%q), status %d; want the booted table", stdout, stderr, status)
	}

	_, stderr, status = run(t, handComposed(t), "invoice:close")
	if status != 1 || !strings.Contains(stderr, "unknown command: invoice:close") || !strings.Contains(stderr, "vendor:publish") {
		t.Errorf("an unknown command exited %d with %q, want 1 and the listing", status, stderr)
	}

	module := &commander{name: "billing", command: "invoice:close"}
	stdout, stderr, status = run(t, handComposed(t, module), "invoice:close")
	if status != 0 || !module.ranBoot || !strings.Contains(stdout, "module command ran") {
		t.Errorf("a module's command: status %d, booted %v, out %q, err %q", status, module.ranBoot, stdout, stderr)
	}
}

// TestAModuleCannotShadowABuiltIn: a module declaring a name the framework
// answers stops the command naming both, whatever command was asked for.
func TestAModuleCannotShadowABuiltIn(t *testing.T) {
	_, stderr, status := run(t, handComposed(t, &commander{name: "billing", command: "routes"}), "version")
	if status != 1 || !strings.Contains(stderr, `"routes"`) || !strings.Contains(stderr, "the framework") ||
		!strings.Contains(stderr, `the module "billing"`) {
		t.Errorf("a module shadowing routes exited %d with %q", status, stderr)
	}
}
