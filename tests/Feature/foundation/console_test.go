package feature

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/arandu-io/framework/foundation"
	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/hesape/auth"
	"github.com/arandu-io/hesape/console"
	"github.com/arandu-io/hesape/exception"
	"github.com/arandu-io/hesape/queue"
	"github.com/arandu-io/hesape/queue/jobs"
)

// configured is an Application composed over the ping-only database, with the
// builder handed to adjust for the test.
func configured(t *testing.T, adjust func(*foundation.ApplicationBuilder), env ...string) (*foundation.Application, error) {
	t.Helper()
	d, err := foundation.NewDriversOver(settingsFor(t, env...), pingDB(t))
	if err != nil {
		t.Fatalf("NewDriversOver: %v", err)
	}
	b := foundation.Configure(d)
	if adjust != nil {
		adjust(b)
	}
	return b.Create()
}

func mustConfigure(t *testing.T, adjust func(*foundation.ApplicationBuilder), env ...string) *foundation.Application {
	t.Helper()
	app, err := configured(t, adjust, env...)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return app
}

// TestTheKernelListsWhatItAnswersAndRefusesWhatItDoesNot: the listing of a
// configured application is every built-in, and an unknown name prints it and
// exits 1.
func TestTheKernelListsWhatItAnswersAndRefusesWhatItDoesNot(t *testing.T) {
	app := mustConfigure(t, nil)

	stdout, _, status := run(t, app, "help")
	if status != 0 {
		t.Fatalf("help exited %d", status)
	}
	for _, name := range []string{
		"serve", "routes", "version", "vendor:publish",
		"migrate", "migrate:rollback", "migrate:fresh", "migrate:status", "db:seed", "db:show", "db:wipe", "model:prune",
		"queue:work", "queue:listen", "queue:failed", "queue:retry", "queue:prune-batches",
		"schedule:list", "schedule:run", "cache:clear", "cache:forget",
	} {
		if !strings.Contains(stdout, "  "+name+" ") {
			t.Errorf("the listing does not name %s:\n%s", name, stdout)
		}
	}

	_, stderr, status := run(t, mustConfigure(t, nil), "invoice:close")
	if status != 1 || !strings.Contains(stderr, "unknown command: invoice:close") || !strings.Contains(stderr, "migrate") {
		t.Errorf("an unknown command exited %d with %q, want 1 and the listing", status, stderr)
	}
}

// TestTheVersionIsWhatTheBuildStamped: version needs nothing booted, and says
// which build is answering.
func TestTheVersionIsWhatTheBuildStamped(t *testing.T) {
	app := mustConfigure(t, func(b *foundation.ApplicationBuilder) { b.WithVersion("v1.2.3", "abc1234") }, "APP_NAME", "billing")
	stdout, stderr, status := run(t, app, "version")
	if status != 0 || strings.TrimSpace(stdout) != "billing v1.2.3 (abc1234)" {
		t.Errorf("version = %q (%q), status %d", stdout, stderr, status)
	}

	stdout, _, _ = run(t, mustConfigure(t, nil, "APP_NAME", "billing"), "version")
	if strings.TrimSpace(stdout) != "billing dev (unknown)" {
		t.Errorf("an unstamped build printed %q", stdout)
	}
}

// TestTheRoutesAreTheApplicationsAndTheFrameworks: routes boots, so the table
// holds what WithRouting registered under web beside the framework's probes;
// route:list is the same command under the name it has elsewhere.
func TestTheRoutesAreTheApplicationsAndTheFrameworks(t *testing.T) {
	web := func(b *foundation.ApplicationBuilder) {
		b.WithRouting(func(r *fhttp.Router) {
			r.Get("/invoices", func(w http.ResponseWriter, _ *http.Request) {}).Name("invoices.index")
		})
	}
	for _, name := range []string{"routes", "route:list"} {
		stdout, stderr, status := run(t, mustConfigure(t, web), name)
		if status != 0 {
			t.Fatalf("%s exited %d: %s", name, status, stderr)
		}
		for _, want := range []string{"/invoices", "invoices.index", "web", "/_arandu/health"} {
			if !strings.Contains(stdout, want) {
				t.Errorf("%s does not print %q:\n%s", name, want, stdout)
			}
		}
	}
}

// TestACommandNameIsAnsweredByOneDeclarer: an application or a module that
// declares a name already taken is refused at creation, naming both, rather
// than shadowing what a deploy step runs.
func TestACommandNameIsAnsweredByOneDeclarer(t *testing.T) {
	_, err := configured(t, func(b *foundation.ApplicationBuilder) {
		b.WithCommands(console.Command{Name: "migrate", Run: func(context.Context, *console.IO) error { return nil }})
	})
	if err == nil || !strings.Contains(err.Error(), `"migrate"`) || !strings.Contains(err.Error(), "the framework") ||
		!strings.Contains(err.Error(), "the application") {
		t.Errorf("an application command named migrate answered %v, want the refusal naming both declarers", err)
	}

	_, err = configured(t, func(b *foundation.ApplicationBuilder) {
		b.WithProviders(&commander{name: "billing", command: "invoice:close"}, &commander{name: "invoicing", command: "invoice:close"})
	})
	if err == nil || !strings.Contains(err.Error(), `the module "billing"`) || !strings.Contains(err.Error(), `the module "invoicing"`) {
		t.Errorf("two modules declaring one name answered %v, want both named", err)
	}

	_, err = configured(t, func(b *foundation.ApplicationBuilder) {
		b.WithCommands(console.Command{Name: "work", Run: func(context.Context, *console.IO) error { return nil }})
	})
	if err == nil || !strings.Contains(err.Error(), `"work"`) {
		t.Errorf("an application command named work answered %v, want it refused as the queue worker's other name", err)
	}

	_, err = configured(t, func(b *foundation.ApplicationBuilder) {
		b.WithCommands(console.Command{Signature: "migrate {--seed}", Run: func(context.Context, *console.IO) error { return nil }})
	})
	if err == nil || !strings.Contains(err.Error(), `"migrate"`) || !strings.Contains(err.Error(), "the framework") ||
		!strings.Contains(err.Error(), "the application") {
		t.Errorf("an application command whose signature names migrate answered %v, want the refusal naming both declarers", err)
	}

	_, err = configured(t, func(b *foundation.ApplicationBuilder) {
		b.WithCommands(console.Command{Signature: "work", Run: func(context.Context, *console.IO) error { return nil }})
	})
	if err == nil || !strings.Contains(err.Error(), `"work"`) {
		t.Errorf("an application command whose signature names work answered %v, want it refused", err)
	}
}

// TestACommandNeedsANameOrASignatureThatParses: a command with neither, or
// with a signature that names nothing, is refused at creation naming who
// declared it.
func TestACommandNeedsANameOrASignatureThatParses(t *testing.T) {
	_, err := configured(t, func(b *foundation.ApplicationBuilder) {
		b.WithCommands(console.Command{Description: "nameless", Run: func(context.Context, *console.IO) error { return nil }})
	})
	if err == nil || !strings.Contains(err.Error(), "the application declared a console command with no name") {
		t.Errorf("a command with neither Name nor Signature answered %v, want it refused", err)
	}

	_, err = configured(t, func(b *foundation.ApplicationBuilder) {
		b.WithCommands(console.Command{Signature: "{user}", Run: func(context.Context, *console.IO) error { return nil }})
	})
	if err == nil || !strings.Contains(err.Error(), "the application") || !strings.Contains(err.Error(), `"{user}"`) {
		t.Errorf("a command whose signature names nothing answered %v, want it refused", err)
	}
}

// TestAnApplicationCommandDeclaredBySignatureAloneIsNamedByIt: WithCommands
// takes a command with an empty Name and a Signature, lists it under the first
// word of the signature, and runs it by that name with its options parsed.
func TestAnApplicationCommandDeclaredBySignatureAloneIsNamedByIt(t *testing.T) {
	var days string
	var sawDays bool
	adjust := func(b *foundation.ApplicationBuilder) {
		b.WithCommands(console.Command{
			Signature:   "notes:prune {--days=90 : Keep this many days}",
			Description: "prune the notes",
			Run: func(_ context.Context, o *console.IO) error {
				days, sawDays = o.Option("days").String(), o.HasOption("days")
				o.Line("pruned")
				return nil
			},
		})
	}

	stdout, stderr, status := run(t, mustConfigure(t, adjust), "help")
	if status != 0 || !strings.Contains(stdout, "  notes:prune ") || !strings.Contains(stdout, "prune the notes") {
		t.Errorf("help exited %d with %q (%q), want notes:prune listed", status, stdout, stderr)
	}

	stdout, stderr, status = run(t, mustConfigure(t, adjust), "notes:prune", "--days=30")
	if status != 0 || !strings.Contains(stdout, "pruned") || !sawDays || days != "30" {
		t.Errorf("notes:prune --days=30: status %d, days %q (declared %v), out %q, err %q", status, days, sawDays, stdout, stderr)
	}
}

// TestTheApplicationsAndTheModulesCommandsRunBooted: what routes/console.go and
// a Commandable module declare reaches services wired at boot, so the kernel
// boots before running them.
func TestTheApplicationsAndTheModulesCommandsRunBooted(t *testing.T) {
	module := &commander{name: "billing", command: "invoice:close"}
	var sawBoot bool
	app := mustConfigure(t, func(b *foundation.ApplicationBuilder) {
		b.WithProviders(module).WithCommands(console.Command{
			Name:        "notes:prune",
			Description: "prune the notes",
			Run: func(_ context.Context, o *console.IO) error {
				sawBoot = module.booted
				o.Line("pruned %s", strings.Join(o.Args(), ","))
				return nil
			},
		})
	})
	stdout, stderr, status := run(t, app, "notes:prune", "--older-than=30")
	if status != 0 || !sawBoot || !strings.Contains(stdout, "pruned --older-than=30") {
		t.Errorf("notes:prune: status %d, booted %v, out %q, err %q", status, sawBoot, stdout, stderr)
	}

	stdout, stderr, status = run(t, mustConfigure(t, func(b *foundation.ApplicationBuilder) { b.WithProviders(module) }), "invoice:close")
	if status != 0 || !module.ranBoot || !strings.Contains(stdout, "module command ran") {
		t.Errorf("invoice:close: status %d, booted %v, out %q, err %q", status, module.ranBoot, stdout, stderr)
	}
}

// TestAnIsolatedCommandWithNothingSharedIsRefusedOutsideDevelopment: a lock
// held inside this process isolates nothing from the replica beside it, so
// migrate is refused in production without a shared store -- and runs in dev,
// where one process is the whole deployment.
func TestAnIsolatedCommandWithNothingSharedIsRefusedOutsideDevelopment(t *testing.T) {
	app := mustConfigure(t, nil, "APP_ENV", "prod", "MAIL_URL", "array://")
	_, stderr, status := run(t, app, "migrate")
	if status != 1 || !strings.Contains(stderr, "migrate takes a lock every replica can see") || !strings.Contains(stderr, "CACHE_STORE=redis") {
		t.Errorf("migrate in prod with nothing shared exited %d with %q", status, stderr)
	}

	// migrate:status reads and takes no lock, so it is not refused for the
	// want of one; on the ping-only database it fails on the query instead.
	_, stderr, _ = run(t, mustConfigure(t, nil, "APP_ENV", "prod", "MAIL_URL", "array://"), "migrate:status")
	if strings.Contains(stderr, "takes a lock") {
		t.Errorf("migrate:status was refused for a lock it does not take: %q", stderr)
	}

	_, stderr, _ = run(t, mustConfigure(t, nil), "migrate")
	if strings.Contains(stderr, "takes a lock") {
		t.Errorf("migrate in dev was refused for its lock: %q", stderr)
	}
}

// TestTheSchemaEmptyingCommandsAreDevelopmentOnly: migrate:fresh is handed the
// environment, and refuses outside dev before it touches anything.
func TestTheSchemaEmptyingCommandsAreDevelopmentOnly(t *testing.T) {
	app := mustConfigure(t, nil, "APP_ENV", "staging")
	_, stderr, status := run(t, app, "migrate:fresh")
	if status == 0 {
		t.Fatal("migrate:fresh ran in staging")
	}
	// Staging with nothing shared refuses on the lock first; either refusal
	// keeps the schema, and both name what to do instead.
	if !strings.Contains(stderr, "lock") && !strings.Contains(stderr, "APP_ENV=dev") {
		t.Errorf("migrate:fresh in staging answered %q", stderr)
	}
}

// TestTheWorkerNeedsTheApplicationsHandlers: queue:work -- and work, the name
// the CLI hands the binary -- builds its worker from WithJobs, and refuses to
// start one with nothing to run.
func TestTheWorkerNeedsTheApplicationsHandlers(t *testing.T) {
	for _, name := range []string{"queue:work", "work"} {
		_, stderr, status := run(t, mustConfigure(t, nil), name, "--once")
		if status == 0 || !strings.Contains(stderr, "handler") {
			t.Errorf("%s with no handlers exited %d with %q, want the refusal", name, status, stderr)
		}
	}

	var registered []string
	app := mustConfigure(t, func(b *foundation.ApplicationBuilder) {
		b.WithJobs(func(w *queue.Worker) error {
			w.HandleFunc("invoice.send", func(context.Context, auth.Grant, *jobs.Job) error { return nil })
			registered = append(registered, "invoice.send")
			return nil
		})
	})
	_, stderr, _ := run(t, app, "queue:work", "--once")
	// Once proven at Create, once on the worker queue:work started.
	if len(registered) != 2 || strings.Contains(stderr, "no job handlers") {
		t.Errorf("WithJobs registered %v and the worker answered %q", registered, stderr)
	}
}

// TestAJobRegistrationThatFailsStopsTheBoot: a handler registration that
// fails is found when the application is created, for every command, rather
// than by the worker alone.
func TestAJobRegistrationThatFailsStopsTheBoot(t *testing.T) {
	_, err := configured(t, func(b *foundation.ApplicationBuilder) {
		b.WithJobs(func(*queue.Worker) error { return errors.New("the webhook handlers need a secret") })
	})
	if err == nil || !strings.Contains(err.Error(), "registering the job handlers") || !strings.Contains(err.Error(), "secret") {
		t.Errorf("a failing registration answered %v", err)
	}
}

// TestTheScheduleIsTheModulesAndTheApplications: the scheduler is composed
// last, over what every module declared and what WithSchedule adds, and
// schedule:list boots to read it.
func TestTheScheduleIsTheModulesAndTheApplications(t *testing.T) {
	task := func(id string) foundation.Task {
		return foundation.Task{ID: id, Spec: "0 3 * * *", Run: func(context.Context, auth.Grant) error { return nil }}
	}
	app := mustConfigure(t, func(b *foundation.ApplicationBuilder) {
		b.WithProviders(&scheduled{tasks: []foundation.Task{task("billing.nightly")}}).
			WithSchedule(task("notes.digest"))
	})
	stdout, stderr, status := run(t, app, "schedule:list")
	if status != 0 || !strings.Contains(stdout, "billing.nightly") || !strings.Contains(stdout, "notes.digest") {
		t.Errorf("schedule:list: status %d, out %q, err %q", status, stdout, stderr)
	}
}

type scheduled struct{ tasks []foundation.Task }

func (*scheduled) Name() string                  { return "billing" }
func (*scheduled) Routes(*fhttp.Router)          {}
func (s *scheduled) Schedule() []foundation.Task { return s.tasks }

// TestTheSeedersAreTheApplications: db:seed hands the application's seed
// function the seeder named on the command line, and says so when there is
// none to hand it to.
func TestTheSeedersAreTheApplications(t *testing.T) {
	var got []string
	app := mustConfigure(t, func(b *foundation.ApplicationBuilder) {
		b.WithSeeders(func(_ context.Context, args []string) (string, error) {
			got = args
			return "UserSeeder", nil
		})
	})
	_, stderr, status := run(t, app, "db:seed", "UserSeeder")
	if status != 0 || len(got) != 1 || got[0] != "UserSeeder" {
		t.Errorf("db:seed UserSeeder: status %d, seeded %v, err %q", status, got, stderr)
	}

	_, stderr, status = run(t, mustConfigure(t, nil), "db:seed")
	if status == 0 || !strings.Contains(stderr, "no seeders") {
		t.Errorf("db:seed with no seeders exited %d with %q", status, stderr)
	}
}

// TestASingleValuedSettingGivenTwiceIsRefused: two answers to one question is
// the one that loses being dropped in silence.
func TestASingleValuedSettingGivenTwiceIsRefused(t *testing.T) {
	seed := func(context.Context, []string) (string, error) { return "", nil }
	tenants := func(context.Context) ([]string, error) { return nil, nil }
	for name, adjust := range map[string]func(*foundation.ApplicationBuilder){
		"WithSeeders": func(b *foundation.ApplicationBuilder) { b.WithSeeders(seed).WithSeeders(seed) },
		"WithTenants": func(b *foundation.ApplicationBuilder) { b.WithTenants(tenants).WithTenants(tenants) },
	} {
		if _, err := configured(t, adjust); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s twice answered %v", name, err)
		}
	}
}

// TestThePipelineIsComposedFromTheConfiguration: the global pipeline answers a
// write without a token, an oversized body and a panic the way the
// configuration says, lets the webhook prefix through to its own guard, and
// runs what the application prepended and appended in their places.
func TestThePipelineIsComposedFromTheConfiguration(t *testing.T) {
	var order []string
	marker := func(name string) fhttp.Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}
	var rendered bool
	app := mustConfigure(t, func(b *foundation.ApplicationBuilder) {
		b.WithMiddleware(func(m *foundation.Middleware) {
			m.ValidateCSRFTokensExcept("/webhooks/").Prepend(marker("prepended")).Append(marker("appended"))
		}).WithExceptions(func(e *exception.Handler) {
			e.ShouldRenderJSONWhen(func(*http.Request, error) bool {
				rendered = true
				return true
			})
		}).WithRouting(func(r *fhttp.Router) {
			ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
			r.Post("/notes", ok)
			r.Post("/webhooks/newsletter", ok)
			r.Get("/explode", func(http.ResponseWriter, *http.Request) { panic("boom") })
		})
	}, "HTTP_MAX_BODY_BYTES", "64")
	if err := app.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	handler := app.Handler()

	var contentType string
	serve := func(method, target, body string) int {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(method, target, strings.NewReader(body)))
		contentType = rec.Header().Get("Content-Type")
		return rec.Code
	}

	if code := serve(http.MethodPost, "/notes", "title=x"); code < 400 || code == http.StatusRequestEntityTooLarge {
		t.Errorf("a write without a CSRF token answered %d", code)
	}
	if code := serve(http.MethodPost, "/webhooks/newsletter", "{}"); code != http.StatusNoContent {
		t.Errorf("the webhook prefix answered %d, want it left to its own guard", code)
	}
	if code := serve(http.MethodPost, "/webhooks/newsletter", strings.Repeat("x", 65)); code != http.StatusRequestEntityTooLarge {
		t.Errorf("a body over HTTP_MAX_BODY_BYTES answered %d, want 413", code)
	}
	if code := serve(http.MethodGet, "/explode", ""); code != http.StatusInternalServerError || !rendered || !strings.Contains(contentType, "json") {
		t.Errorf("a panic answered %d as %q (asked %v), want 500 drawn the way the application's exceptions said", code, contentType, rendered)
	}

	order = nil
	serve(http.MethodPost, "/webhooks/newsletter", "{}")
	if strings.Join(order, ",") != "prepended,appended" {
		t.Errorf("the application's middleware ran as %v", order)
	}
}
