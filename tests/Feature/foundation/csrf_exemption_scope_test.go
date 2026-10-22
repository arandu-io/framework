package feature

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/arandu-io/framework/foundation"
	fhttp "github.com/arandu-io/framework/http"
)

// TestAModulesCSRFExemptionCoversOnlyItsOwnRoutes: an exemption a module
// declares that also covers a write route of the application stops the boot
// naming the module, the exemption and the route, instead of turning the CSRF
// check off for a route nobody exempted.
func TestAModulesCSRFExemptionCoversOnlyItsOwnRoutes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		exempt string
		route  func(r *fhttp.Router)
		want   string
	}{
		{"a subtree over another route", "/hooks/", func(r *fhttp.Router) { r.Post("/hooks/manage/rotate", noContent) }, `"/hooks/manage/rotate"`},
		{"a parameter over another route", "/hooks/{name}", func(r *fhttp.Router) { r.Delete("/hooks/{id}", noContent) }, `"/hooks/{id}"`},
		{"a remainder under the exemption", "/hooks/acme/", func(r *fhttp.Router) { r.Patch("/hooks/{rest...}", noContent) }, `"/hooks/{rest...}"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, err := configured(t, func(b *foundation.ApplicationBuilder) {
				b.WithProviders(hookModule{exempt: []string{tc.exempt}}).WithRouting(tc.route)
			})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			t.Cleanup(func() { _ = app.Shutdown() })
			err = app.Boot(context.Background())
			if err == nil {
				t.Fatalf("the exemption %q of the module covered an application route and was accepted", tc.exempt)
			}
			for _, want := range []string{`module "acme"`, `"` + tc.exempt + `"`, tc.want, `module "web"`} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("Boot error = %q, want it to name %s", err, want)
				}
			}
		})
	}

	// A read is not checked for a token, and a write the exemption does not
	// reach is not covered: both boot.
	app, err := configured(t, func(b *foundation.ApplicationBuilder) {
		b.WithProviders(hookModule{exempt: []string{"/hooks/acme"}}).WithRouting(func(r *fhttp.Router) {
			r.Get("/hooks/{name}", noContent)
			r.Post("/hooks/acme/rotate", noContent)
			r.Post("/hooksx", noContent)
		})
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _ = app.Shutdown() })
	if err := app.Boot(context.Background()); err != nil {
		t.Fatalf("Boot refused an exemption that covers only its module's route: %v", err)
	}
}

// TestTheCSRFExemptionsOfModulesAreLogged: what a module exempts is written to
// the log at boot, with the module, so a broad prefix is visible to whoever
// reads the deployment's log.
func TestTheCSRFExemptionsOfModulesAreLogged(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	stdout := os.Stdout
	os.Stdout = w
	app, err := configured(t, func(b *foundation.ApplicationBuilder) {
		b.WithProviders(hookModule{exempt: []string{"/hooks/"}})
	})
	if err == nil {
		err = app.Boot(context.Background())
		t.Cleanup(func() { _ = app.Shutdown() })
	}
	os.Stdout = stdout
	_ = w.Close()
	out, _ := io.ReadAll(r)
	if err != nil {
		t.Fatalf("Boot: %v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, `msg="csrf exemption"`) && strings.Contains(line, "module=acme") && strings.Contains(line, "path=/hooks/") {
			return
		}
	}
	t.Fatalf("Boot logged no line for the module's exemption:\n%s", out)
}
