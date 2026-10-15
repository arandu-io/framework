package feature

import (
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/http/middleware"
	fwebhook "github.com/arandu-io/framework/webhook"
	"github.com/arandu-io/hesape/database/migrations"
	hwebhook "github.com/arandu-io/hesape/webhook"
)

// everyRoute is a request to each management route, under the default
// prefix. TestTheModuleRegistersItsRoutesUnderItsPrefix fails when a route
// exists that this list does not reach.
var everyRoute = []struct {
	method string
	target string
	body   string
}{
	{http.MethodGet, hwebhook.DefaultPrefix + "/endpoints", ""},
	{http.MethodPost, hwebhook.DefaultPrefix + "/endpoints", validForm},
	{http.MethodGet, hwebhook.DefaultPrefix + "/endpoints/e-1", ""},
	{http.MethodPut, hwebhook.DefaultPrefix + "/endpoints/e-1", validForm},
	{http.MethodDelete, hwebhook.DefaultPrefix + "/endpoints/e-1", ""},
	{http.MethodGet, hwebhook.DefaultPrefix + "/endpoints/e-1/secret", ""},
	{http.MethodPost, hwebhook.DefaultPrefix + "/endpoints/e-1/test", ""},
	{http.MethodGet, hwebhook.DefaultPrefix + "/endpoints/e-1/deliveries", ""},
	{http.MethodPost, hwebhook.DefaultPrefix + "/endpoints/e-1/rotate", ""},
	{http.MethodPost, hwebhook.DefaultPrefix + "/endpoints/e-1/enable", ""},
	{http.MethodPost, hwebhook.DefaultPrefix + "/endpoints/e-1/disable", ""},
	{http.MethodGet, hwebhook.DefaultPrefix + "/deliveries/d-1", ""},
	{http.MethodGet, hwebhook.DefaultPrefix + "/deliveries/d-1/attempts", ""},
	{http.MethodPost, hwebhook.DefaultPrefix + "/deliveries/d-1/redeliver", ""},
	{http.MethodGet, hwebhook.DefaultPrefix + "/calls", ""},
	{http.MethodGet, hwebhook.DefaultPrefix + "/calls/c-1", ""},
	{http.MethodPost, hwebhook.DefaultPrefix + "/calls/c-1/process", ""},
}

// validForm is an endpoint that passes validation, so the request reaches the
// policy.
const validForm = "name=Billing&url=https%3A%2F%2Fexample.com%2Fhooks&events=order.paid&active=1"

func TestAVisitorWithNoSessionIsSentToSignIn(t *testing.T) {
	t.Parallel()

	m := mount(t, hwebhook.Config{})

	// RequireAuth answers before the handler runs, so a visitor with no
	// session reaches neither the policy nor the database: the answer is the
	// sign-in screen, not a refusal from a policy that was asked about nobody.
	for _, request := range everyRoute {
		rec := m.answer(t, request.method, request.target, request.body)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != middleware.SignInPath {
			t.Errorf("%s %s answered %d to %q, want %d to %s",
				request.method, request.target, rec.Code, rec.Header().Get("Location"),
				http.StatusSeeOther, middleware.SignInPath)
		}
	}
}

func TestASignedInSubjectReachesThePolicyAndIsRefused(t *testing.T) {
	t.Parallel()

	m := mount(t, hwebhook.Config{})
	cookies := m.signIn(t, administrator())

	// The guard admits the session and puts its subject on the request; the
	// handler hands it to the service, and the closed policy refuses it. The
	// Framework adapter turns that refusal into 403 -- before any statement,
	// which the handle over no database would have panicked on.
	for _, request := range everyRoute {
		rec := m.answer(t, request.method, request.target, request.body, cookies...)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s answered %d for a signed-in administrator, want %d",
				request.method, request.target, rec.Code, http.StatusForbidden)
		}
	}
}

func TestARejectedInputGoesBackBeforeTheDatabase(t *testing.T) {
	t.Parallel()

	m := mount(t, hwebhook.Config{})
	cookies := m.signIn(t, administrator())

	// The input is validated before anything is authorized, so this refusal
	// comes from the request and not from the policy. The Framework adapter
	// answers it the way it answers every rejected form: back where it came
	// from, with the messages in the flash -- and it still never reaches a
	// statement.
	rec := m.answer(t, http.MethodPost, hwebhook.DefaultPrefix+"/endpoints", "name=", cookies...)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("an empty name answered %d to %q, want %d back to /", rec.Code, rec.Header().Get("Location"), http.StatusSeeOther)
	}

	// The page the redirect lands on is a navigation, which is the only read
	// the flash spends itself on.
	next := httptest.NewRequest(http.MethodGet, "/", nil)
	next.Header.Set("Accept", "text/html")
	for _, cookie := range rec.Result().Cookies() {
		next.AddCookie(cookie)
	}
	errs, _, ok := m.flash.Take(httptest.NewRecorder(), next)
	if !ok || len(errs["name"]) == 0 {
		t.Fatalf("the redirect carries no message for name in the flash: %v", errs)
	}
}

// TestTheRouterAnswersAMissingRecordWith404 holds that the router the bridge
// registers on answers the native ErrNotFound, with no mapping written in
// either package.
func TestTheRouterAnswersAMissingRecordWith404(t *testing.T) {
	t.Parallel()

	router := fhttp.NewRouter()
	router.Action(http.MethodGet, "/missing", func(*fhttp.Context) error {
		return hwebhook.ErrNotFound
	})

	if rec := serve(t, router, httptest.NewRequest(http.MethodGet, "/missing", nil)); rec.Code != http.StatusNotFound {
		t.Fatalf("ErrNotFound answered %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestTheModuleRegistersItsRoutesUnderItsPrefix(t *testing.T) {
	t.Parallel()

	m := mount(t, hwebhook.Config{Prefix: "/widgets"})

	got := make([]string, 0, len(everyRoute))
	for _, route := range m.router.Routes() {
		if route.Module != "webhook" {
			t.Errorf("the route %s %s is not tagged with the module name: %q", route.Method, route.Pattern, route.Module)
		}
		got = append(got, route.Method+" "+route.Pattern+" "+route.RouteName())
	}
	sort.Strings(got)

	want := make([]string, 0, len(publishedManagementRoutes))
	for _, route := range publishedManagementRoutes {
		pattern := "/widgets" + strings.TrimPrefix(route.pattern, hwebhook.DefaultPrefix)
		want = append(want, route.method+" "+pattern+" "+route.name)
	}
	sort.Strings(want)

	if len(got) != len(want) || len(got) != len(everyRoute) {
		t.Fatalf("registered %v, want %v, and everyRoute reaches %d of them", got, want, len(everyRoute))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("registered %v, want %v", got, want)
		}
	}
}

// TestNoRouteLandsInTheFrameworkNamespace registers the routes and reads the
// table back, because a prefix arrives through configuration and the only way
// to know where the routes ended up is to look. Both the default and a
// configured prefix are mounted, with a receiver, because each reaches the
// router by a different path.
func TestNoRouteLandsInTheFrameworkNamespace(t *testing.T) {
	t.Parallel()

	for _, cfg := range []hwebhook.Config{
		{Receivers: []hwebhook.Receiver{billing()}},
		{Prefix: "/widgets", ReceivePrefix: "/inbound", Receivers: []hwebhook.Receiver{billing()}},
	} {
		m := mount(t, cfg)

		registered := 0
		for _, route := range m.router.Routes() {
			registered++
			if route.Pattern == reservedPrefix || strings.HasPrefix(route.Pattern, reservedPrefix+"/") {
				t.Errorf("the route %s %s is registered under %s/, which the framework keeps for itself and refuses at boot",
					route.Method, route.Pattern, reservedPrefix)
			}
		}
		if registered == 0 {
			t.Fatal("the module registered no route, so this test proved nothing")
		}
	}
}

func TestNewRefusesAWiringThatCannotWork(t *testing.T) {
	t.Parallel()

	guard := middleware.RequireAuth(newSessions())
	handle := noDatabase()
	valid := hwebhook.Config{Key: []byte(appKey)}

	if _, err := hwebhook.New(hwebhook.Config{Key: []byte(appKey), PageSize: -1}, handle, guard); err == nil {
		t.Error("a configuration with a negative page size was accepted")
	}
	if _, err := hwebhook.New(hwebhook.Config{}, handle, guard); err == nil {
		t.Error("a configuration with no key was accepted")
	}
	if _, err := hwebhook.New(valid, nil, guard); err == nil {
		t.Error("a nil database handle was accepted")
	}
	if _, err := hwebhook.New(valid, handle, guard); err != nil {
		t.Fatalf("a valid wiring was refused: %v", err)
	}

	// The bridge refuses a module that was never built, at wiring.
	func() {
		defer func() {
			if recover() == nil {
				t.Error("NewModule accepted a nil module")
			}
		}()
		fwebhook.NewModule(nil)
	}()

	// With no guard the module mounts its receivers and no management route.
	receiver := billing()
	receiver.Methods = nil
	native, err := hwebhook.New(hwebhook.Config{Key: []byte(appKey), Receivers: []hwebhook.Receiver{receiver}}, handle, nil)
	if err != nil {
		t.Fatalf("a wiring with no guard was refused: %v", err)
	}
	module := fwebhook.NewModule(native)
	router := fhttp.NewRouter()
	module.Routes(router.ForModule(module.Name()))
	routes := router.Routes()
	if len(routes) != 1 || routes[0].Pattern != "/webhooks/billing" || routes[0].Method != http.MethodPost || routes[0].RouteName() != "webhook.receive.billing" {
		t.Fatalf("with no guard the module registered %v", routes)
	}
}

func TestNewRefusesARoutePrefixThatCannotBeRegistered(t *testing.T) {
	t.Parallel()

	guard := middleware.RequireAuth(newSessions())
	for _, prefix := range []string{"/widgets{", "/widgets/{id}"} {
		if _, err := hwebhook.New(hwebhook.Config{Key: []byte(appKey), Prefix: prefix}, noDatabase(), guard); err == nil {
			t.Errorf("New accepted route prefix %q, which would panic during route registration", prefix)
		}
	}
}

func TestTheModuleDeclaresItsSchema(t *testing.T) {
	t.Parallel()

	m := mount(t, hwebhook.Config{})
	declared := fwebhook.NewModule(m.native).Migrations()
	if len(declared) == 0 {
		t.Fatal("the module returns no migration")
	}

	native := m.native.Migrations()
	names := make([]string, 0, len(declared))
	for i, migration := range declared {
		name := migration.GetName()
		if name == "" {
			t.Fatal("a migration has no name, and the name is what carries the order")
		}
		if i >= len(native) || native[i].GetName() != name {
			t.Fatalf("the bridge declares %s at %d, where the native module declares something else", name, i)
		}
		names = append(names, name)

		// A migration that cannot be rolled back is a deploy that cannot be
		// undone. The migrator finds Down by type assertion, so a Down with
		// the wrong signature is a rollback that silently does nothing.
		if _, ok := migration.(migrations.ReversibleMigration); !ok {
			t.Errorf("the migration %s has no Down", name)
		}
	}
	if len(declared) != len(native) {
		t.Fatalf("the bridge declares %d migrations and the native module %d", len(declared), len(native))
	}

	if !sort.StringsAreSorted(names) {
		t.Fatalf("the migrations are not returned in the order their names sort in: %v", names)
	}
}
