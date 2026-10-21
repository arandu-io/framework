package feature

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/arandu-io/framework/foundation"
	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/hesape/auth"
	"github.com/arandu-io/hesape/queue"
	"github.com/arandu-io/hesape/queue/jobs"
	"github.com/arandu-io/hesape/routing"
)

// noContent answers every request it is handed with 204.
func noContent(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }

// hookModule receives a provider's callbacks: a framework module that declares
// the prefix they arrive under.
type hookModule struct{ exempt []string }

func (hookModule) Name() string { return "acme" }

func (hookModule) Routes(r *fhttp.Router) { r.Post("/hooks/acme", noContent) }

func (m hookModule) CSRFExemptPaths() []string { return m.exempt }

var _ foundation.CSRFExempt = hookModule{}

// pingModule is a hesape module on the handler-only router, registered through
// Component, that declares one exact path.
type pingModule struct{}

func (pingModule) Name() string { return "feed-ping" }

func (pingModule) Routes(r *routing.Router) { r.Post("/feed/ping", http.HandlerFunc(noContent)) }

func (pingModule) CSRFExemptPaths() []string { return []string{"/feed/ping"} }

// TestAModulesCSRFExemptionsNeedNoLineInTheApplication: what a provider
// declares is exempted from the CSRF check, through Component too, while every
// other write is still checked and the application's own exemptions still hold.
func TestAModulesCSRFExemptionsNeedNoLineInTheApplication(t *testing.T) {
	app := bootConfigured(t, func(b *foundation.ApplicationBuilder) {
		b.WithProviders(hookModule{exempt: []string{"/hooks/"}}, foundation.Component(pingModule{})).
			WithMiddleware(func(m *foundation.Middleware) { m.ValidateCSRFTokensExcept("/v1/usage") }).
			WithRouting(func(r *fhttp.Router) {
				r.Post("/notes", noContent)
				r.Post("/v1/usage", noContent)
				r.Post("/feed/ping/x", noContent)
			})
	})

	for path, exempt := range map[string]bool{
		"/hooks/acme": true, "/feed/ping": true, "/v1/usage": true, "/notes": false, "/feed/ping/x": false,
	} {
		rec := serveConfigured(t, app, httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}")))
		if got := rec.Code == http.StatusNoContent; got != exempt {
			t.Errorf("POST %s answered %d; exempt = %v, want %v", path, rec.Code, got, exempt)
		}
	}
}

// TestAModuleCannotTurnTheCSRFCheckOff: an exemption the check refuses stops
// Create naming the module, rather than panicking or exempting every route.
func TestAModuleCannotTurnTheCSRFCheckOff(t *testing.T) {
	for _, path := range []string{"/", "", "hooks/", "/hooks/../notes"} {
		_, err := configured(t, func(b *foundation.ApplicationBuilder) {
			b.WithProviders(hookModule{exempt: []string{path}})
		})
		if err == nil || !strings.Contains(err.Error(), `module "acme"`) || !strings.Contains(err.Error(), "CSRF") {
			t.Errorf("a module exempting %q answered %v, want the refusal naming it", path, err)
		}
	}
}

// jobsModule runs jobs of its own: a framework module that registers their
// handlers, and counts how often it was asked.
type jobsModule struct {
	name  string
	jobs  []string
	asked *int
}

func (m jobsModule) Name() string { return m.name }

func (jobsModule) Routes(*fhttp.Router) {}

func (m jobsModule) RegisterJobHandlers(w *queue.Worker) error {
	if m.asked != nil {
		*m.asked++
	}
	for _, name := range m.jobs {
		w.HandleFunc(name, func(context.Context, auth.Grant, *jobs.Job) error { return nil })
	}
	return nil
}

var _ foundation.JobHandlers = jobsModule{}

// pingJobs is a hesape module on the handler-only router whose handlers reach
// the worker through Component.
type pingJobs struct{ asked *int }

func (pingJobs) Name() string { return "feed-jobs" }

func (pingJobs) Routes(*routing.Router) {}

func (m pingJobs) RegisterJobHandlers(w *queue.Worker) error {
	*m.asked++
	w.HandleFunc("feed.ping", func(context.Context, auth.Grant, *jobs.Job) error { return nil })
	return nil
}

// TestAModulesJobHandlersNeedNoLineInTheApplication: the worker queue:work
// starts runs what a provider registers -- through Component too -- with no
// WithJobs at all, so it is not refused for having nothing to run.
func TestAModulesJobHandlersNeedNoLineInTheApplication(t *testing.T) {
	var direct, wrapped int
	app := mustConfigure(t, func(b *foundation.ApplicationBuilder) {
		b.WithProviders(
			jobsModule{name: "acme", jobs: []string{"acme.deliver"}, asked: &direct},
			foundation.Component(pingJobs{asked: &wrapped}),
		)
	})
	_, stderr, _ := run(t, app, "queue:work", "--once")
	if strings.Contains(stderr, "no job handlers") {
		t.Fatalf("a worker over two modules' handlers was refused: %q", stderr)
	}
	// Once proven at Create, once on the worker queue:work started.
	if direct != 2 || wrapped != 2 {
		t.Errorf("the modules were asked %d and %d times, want 2 each (stderr %q)", direct, wrapped, stderr)
	}
}

// TestAJobNameGivenTwoHandlersStopsTheBootNamingBoth: a name a module and the
// application both register, or two modules, or one module twice, refuses
// Create naming who registered it -- never a panic, never one silently winning.
func TestAJobNameGivenTwoHandlersStopsTheBootNamingBoth(t *testing.T) {
	cases := map[string]struct {
		adjust func(*foundation.ApplicationBuilder)
		want   []string
	}{
		"module and application": {
			adjust: func(b *foundation.ApplicationBuilder) {
				b.WithProviders(jobsModule{name: "acme", jobs: []string{"acme.deliver"}}).
					WithJobs(func(w *queue.Worker) error {
						w.HandleFunc("acme.deliver", func(context.Context, auth.Grant, *jobs.Job) error { return nil })
						return nil
					})
			},
			want: []string{`"acme.deliver"`, "the application", `the module "acme"`},
		},
		"two modules": {
			adjust: func(b *foundation.ApplicationBuilder) {
				b.WithProviders(
					jobsModule{name: "acme", jobs: []string{"invoice.send"}},
					jobsModule{name: "billing", jobs: []string{"invoice.send"}},
				)
			},
			want: []string{`"invoice.send"`, `the module "acme"`, `the module "billing"`},
		},
		"one module twice": {
			adjust: func(b *foundation.ApplicationBuilder) {
				b.WithProviders(jobsModule{name: "acme", jobs: []string{"acme.deliver", "acme.deliver"}})
			},
			want: []string{"acme.deliver", `the module "acme"`},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := configured(t, c.adjust)
			if err == nil {
				t.Fatal("Create accepted a job name with two handlers")
			}
			for _, want := range c.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal %q does not name %s", err, want)
				}
			}
		})
	}
}
