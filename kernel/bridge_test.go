// Tests of the bridge, and of nothing else.
//
// What each symbol DOES is tested in github.com/arandu-io/framework/foundation,
// against the code that now runs, and for the module vocabulary in
// github.com/arandu-io/hesape/foundation beyond that. The unit tests that used
// to live here moved with the implementation; keeping a second copy would be a
// second place for the behaviour to be described.
//
// What is left to prove is the only thing this package still claims: that the
// old name reaches the new behaviour. That is one assertion per alias -- the
// compiler makes it, so it is written as an assignment -- and one round trip
// per wrapper, because a wrapper is the place a name can be wired to the wrong
// function and still compile.

package kernel_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/framework/foundation"
	"github.com/arandu-io/framework/foundation/bootstrap"
	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/kernel"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/config"
	"github.com/arandu-io/hesape/encryption"
	hfoundation "github.com/arandu-io/hesape/foundation"
)

// TestKernelIsTheApplication is the rename this bridge exists for.
//
// An alias and not a defined type: a *Kernel built here is the same value a
// package written against *foundation.Application receives, with the same
// methods, so nothing had to be forwarded by hand.
func TestKernelIsTheApplication(t *testing.T) {
	if reflect.TypeFor[kernel.Kernel]() != reflect.TypeFor[foundation.Application]() {
		t.Fatal("kernel.Kernel stopped being foundation.Application")
	}

	var _ *foundation.Application = kernel.New(testConfig(config.EnvProd))
}

// TestTheModuleVocabularyIsTheHesapeVocabulary walks the aliases of aliases.
//
// Everything here is forwarded twice: kernel to foundation, and foundation to
// github.com/arandu-io/hesape/foundation. A rename in hesape that this chain
// has not followed fails on this line rather than in the repositories that
// import the old names.
func TestTheModuleVocabularyIsTheHesapeVocabulary(t *testing.T) {
	for name, pair := range map[string][2]reflect.Type{
		"Bootable":    {reflect.TypeFor[kernel.Bootable](), reflect.TypeFor[hfoundation.Bootable]()},
		"Background":  {reflect.TypeFor[kernel.Background](), reflect.TypeFor[hfoundation.Background]()},
		"Closable":    {reflect.TypeFor[kernel.Closable](), reflect.TypeFor[hfoundation.Closable]()},
		"Diagnostic":  {reflect.TypeFor[kernel.Diagnostic](), reflect.TypeFor[hfoundation.Diagnostic]()},
		"Health":      {reflect.TypeFor[kernel.Health](), reflect.TypeFor[hfoundation.Health]()},
		"Schedulable": {reflect.TypeFor[kernel.Schedulable](), reflect.TypeFor[hfoundation.Schedulable]()},
		"Migratable":  {reflect.TypeFor[kernel.Migratable](), reflect.TypeFor[hfoundation.Migratable]()},
		"Migration":   {reflect.TypeFor[kernel.Migration](), reflect.TypeFor[hfoundation.Migration]()},
		"Task":        {reflect.TypeFor[kernel.Task](), reflect.TypeFor[hfoundation.Task]()},
		"Scope":       {reflect.TypeFor[kernel.Scope](), reflect.TypeFor[hfoundation.Scope]()},
		"ReloadTagger": {
			reflect.TypeFor[kernel.ReloadTagger](),
			reflect.TypeFor[hfoundation.ReloadTagger](),
		},
	} {
		if pair[0] != pair[1] {
			t.Errorf("kernel.%s is %s and hesape/foundation.%s is %s", name, pair[0], name, pair[1])
		}
	}

	if kernel.Global != hfoundation.Global || kernel.PerTenant != hfoundation.PerTenant {
		t.Error("the Scope constants stopped being the hesape ones")
	}
}

// TestTaskCarriesTheGrantUnchanged is the field the alias could have broken
// silently.
//
// hesape/foundation.Task names auth.Action and auth.Grant; the old name here
// named security.Action and security.Grant. Those are already aliases of each
// other, which is the only reason Task could be aliased at all -- a task
// written against the old names has to remain assignable to the new type.
func TestTaskCarriesTheGrantUnchanged(t *testing.T) {
	var got security.Grant
	task := kernel.Task{
		ID:      "invoice.close",
		Spec:    "0 3 * * *",
		Scope:   kernel.PerTenant,
		Timeout: time.Minute,
		Action:  security.Action("invoice.close"),
		Run: func(ctx context.Context, g security.Grant) error {
			got = g
			return nil
		},
	}

	var _ hfoundation.Task = task

	if err := task.Run(context.Background(), security.SystemGrant("invoice.close", "acme")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Grant holds only unexported fields and is not comparable, so what is
	// asserted is what it carries: the action it authorizes and the tenant a
	// statement takes from it.
	if got.Action() != security.Action("invoice.close") {
		t.Errorf("the action did not survive the alias: %q", got.Action())
	}
	if got.Subject().Tenant != "acme" {
		t.Errorf("the tenant did not survive the alias: %q", got.Subject().Tenant)
	}
}

// TestTheThreeDeclaredNamesAreTheFoundationOnes covers what foundation declares
// rather than forwards. Module keeps a *fhttp.Router, so a module written
// against the old name still compiles; Locker is the interface events.Locker
// aliases, so one value wires into both the relay and the scheduler.
func TestTheThreeDeclaredNamesAreTheFoundationOnes(t *testing.T) {
	for name, pair := range map[string][2]reflect.Type{
		"Module":           {reflect.TypeFor[kernel.Module](), reflect.TypeFor[foundation.Module]()},
		"RendererProvider": {reflect.TypeFor[kernel.RendererProvider](), reflect.TypeFor[foundation.RendererProvider]()},
		"Locker":           {reflect.TypeFor[kernel.Locker](), reflect.TypeFor[foundation.Locker]()},
	} {
		if pair[0] != pair[1] {
			t.Errorf("kernel.%s is %s and foundation.%s is %s", name, pair[0], name, pair[1])
		}
	}

	var _ kernel.Module = probe{}
	var _ kernel.Locker = lock{}
}

// TestNewStillBootsAndServes is the round trip for the one wrapper every
// project calls. The signature is unchanged, and what it answers has to be an
// application that boots, registers a module's routes and serves them.
func TestNewStillBootsAndServes(t *testing.T) {
	k := kernel.New(testConfig(config.EnvProd)).Register(probe{})

	if err := k.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	t.Cleanup(func() { _ = k.Shutdown() })

	rec := httptest.NewRecorder()
	k.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/probe", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

// TestFormatRoutesReachesTheHesapeTable is the round trip for the second
// wrapper. The rows come from hesape/routing now, and a table grouped by
// something other than the module would mean the wrapper reached the wrong
// function.
func TestFormatRoutesReachesTheHesapeTable(t *testing.T) {
	k := kernel.New(testConfig(config.EnvProd)).Register(probe{})
	if err := k.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	t.Cleanup(func() { _ = k.Shutdown() })

	out := kernel.FormatRoutes(k.Routes())
	if !strings.Contains(out, "probe\n  GET     /probe") {
		t.Errorf("routes are not grouped under their module:\n%s", out)
	}
	if got := kernel.FormatRoutes(nil); !strings.Contains(got, "no routes") {
		t.Errorf("FormatRoutes(nil) = %q", got)
	}
}

// probe is a module of the shape every project writes: a name and routes taking
// the *fhttp.Router the old contract named.
type probe struct{}

func (probe) Name() string { return "probe" }

func (probe) Routes(r *fhttp.Router) {
	r.Get("/probe", func(w http.ResponseWriter, req *http.Request) { w.WriteHeader(http.StatusOK) })
}

// lock is a Locker of the shape an application supplies.
type lock struct{}

func (lock) Run(ctx context.Context, name string, ttl time.Duration, fn func(context.Context) error) error {
	return fn(ctx)
}

func testConfig(env config.Env) bootstrap.Configuration {
	return bootstrap.Configuration{
		App: config.App{
			Name:     "bridge",
			Env:      env,
			HTTPAddr: ":0",
			Key:      make([]byte, encryption.KeySize),
		},
		Observability: bootstrap.Observability{LogLevel: slog.LevelError},
	}
}
