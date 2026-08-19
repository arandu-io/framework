package framework_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/arandu-io/framework/foundation"
	"github.com/arandu-io/framework/foundation/bootstrap"
	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/modules/auth"
	"github.com/arandu-io/framework/view"
	"github.com/arandu-io/hesape/config"
	"github.com/arandu-io/hesape/encryption"
)

// inventoryConfig is the configuration the tests below read and nothing else.
// It opens no connection and binds no port.
func inventoryConfig(env config.Env, secret string) bootstrap.Configuration {
	return bootstrap.Configuration{
		App: config.App{
			Name:     "test",
			Env:      env,
			HTTPAddr: ":0",
			Key:      make([]byte, encryption.KeySize),
		},
		Observability: bootstrap.Observability{
			LogLevel:      slog.LevelError,
			TracingSecret: secret,
		},
	}
}

// everyModuleThatRegistersRoutes returns the modules this collection ships that
// put a route in an application: the view module and the authentication module.
//
// The authentication service is built without a database on purpose. Nothing
// below sends a request to a handler that would reach one, and a repository that
// holds a nil handle registers the same three routes as one that holds an open
// pool.
func everyModuleThatRegistersRoutes() []foundation.Module {
	return []foundation.Module{
		view.NewModule(),
		auth.New(auth.NewService(auth.NewUserRepo(nil), nil, nil), nil),
	}
}

// inventoryOf returns one line per registered route, sorted the way the route
// table is printed, so the comparison does not depend on the order the modules
// happened to be registered in.
//
// The line carries all four facts a row holds: the method, the pattern, the
// module that registered it, and the name it can be addressed by. The name is in
// there because it is a promise -- once a route has one, a URL is built from it
// and renaming it breaks every caller -- so a route that gains one has changed
// what it offers even though the path is untouched.
func inventoryOf(routes []*fhttp.Route) []string {
	out := make([]string, 0, len(routes))
	for _, r := range routes {
		out = append(out, fmt.Sprintf("%s %s [%s] %q", r.Method, r.Pattern, r.Module, r.RouteName()))
	}
	sort.Strings(out)
	return out
}

// TestTheRouteTableIsTheFullExpectedInventory writes down every route this
// collection puts into an application, with no filter of any kind.
//
// The route table is the inventory: it is what route introspection prints and
// what an audit of "what does this process answer" reads. An inventory nobody
// asserts on grows by accident -- a module gains an endpoint, the table gains a
// row, and the first person to find out is whoever is scanning the deployment.
// This is the assertion that stops that: a new route fails here until somebody
// writes it down, and writing it down is the moment to ask what gates it.
//
// It covers the whole table rather than one prefix, which is what the surface
// mounted under the internal prefix already had. Three of the eight rows sit
// outside that prefix and were covered by nothing, and one of the rows inside it
// is registered by the view module rather than by the Application -- so the
// prefix is not a synonym for "what the Application mounts", and reading only
// one of the two misses whichever routes the other owns.
//
// The environment decides the surface, so all three shapes are here. The debug
// console is mounted only where a recorder exists, and the development reload
// only in development.
func TestTheRouteTableIsTheFullExpectedInventory(t *testing.T) {
	const (
		authLogin  = `GET /auth/login [auth] ""`
		authSubmit = `POST /auth/login [auth] ""`
		authLogout = `POST /auth/logout [auth] ""`
		assets     = `GET /_arandu/assets/{hash}/{name} [view] ""`
		health     = `GET /_arandu/health [arandu] ""`
		reload     = `GET /_arandu/reload [arandu] ""`
		console    = `GET /_arandu/debug [arandu] ""`
		consoleOne = `GET /_arandu/debug/{id} [arandu] ""`
	)

	for _, tc := range []struct {
		name   string
		env    config.Env
		secret string
		want   []string
	}{
		{
			name: "production without a tracing secret",
			env:  config.EnvProd,
			want: []string{health, authLogin, authSubmit, authLogout, assets},
		},
		{
			name:   "production with a tracing secret",
			env:    config.EnvProd,
			secret: "secret",
			want:   []string{health, console, consoleOne, authLogin, authSubmit, authLogout, assets},
		},
		{
			name: "development",
			env:  config.EnvDev,
			want: []string{health, reload, console, consoleOne, authLogin, authSubmit, authLogout, assets},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := foundation.New(inventoryConfig(tc.env, tc.secret)).
				Register(everyModuleThatRegistersRoutes()...)
			if err := a.Boot(context.Background()); err != nil {
				t.Fatalf("Boot: %v", err)
			}

			got := inventoryOf(a.Routes())
			want := append([]string(nil), tc.want...)
			sort.Strings(want)

			if len(got) != len(want) {
				t.Fatalf("the route table changed:\ngot  %v\nwant %v", got, want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("route %d = %s, want %s", i, got[i], want[i])
				}
			}
		})
	}
}

// pipelineProbe is a module with one ordinary route and the middleware that
// records which requests reached it.
//
// The two live on one type because the assertion is about the pair: what the
// application registered, and what the application's own middleware saw.
type pipelineProbe struct{ seen []string }

// Name is the module identifier.
func (*pipelineProbe) Name() string { return "probe" }

// Routes registers one route outside the internal prefix, as an application's
// own route would be.
func (*pipelineProbe) Routes(r *fhttp.Router) {
	r.Get("/probe", func(w http.ResponseWriter, req *http.Request) { w.WriteHeader(http.StatusOK) })
}

// middleware records the path of every request that reaches it.
func (p *pipelineProbe) middleware() fhttp.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p.seen = append(p.seen, r.URL.Path)
			next.ServeHTTP(w, r)
		})
	}
}

// TestTheInternalPrefixDecidesWhatTheApplicationsMiddlewareSees measures the
// half of the inventory the table does not show: which of those routes answer
// without the application's pipeline.
//
// Every middleware an application installs is wrapped so that it does not run
// under the internal prefix, which means a request there is answered with no
// Recover, no request logging, no security headers, no rate limit and no CSRF
// check. That is deliberate for the health probe -- a probe throttled by the
// application it reports on reports the wrong thing -- and it is what makes the
// set worth asserting rather than describing.
//
// Four requests, one question each:
//
//   - an ordinary route is seen, which is the pipeline doing its job;
//   - the health probe is not, which is the exemption working;
//   - the asset route is not either, and it is registered by the view module
//     rather than by the Application;
//   - a path that only looks internal is seen like any other.
//
// The third is the finding and the fourth is what makes it legible. The
// exemption reads the path, not the registration, so what escapes the pipeline
// is whatever is spelled under the prefix -- and the prefix is a string constant
// in one package while the asset path is a string constant in another. Nothing
// ties the two together, so the set that escapes is held apart by two literals
// agreeing, and this is where they are checked against each other.
func TestTheInternalPrefixDecidesWhatTheApplicationsMiddlewareSees(t *testing.T) {
	probe := &pipelineProbe{}
	a := foundation.New(inventoryConfig(config.EnvProd, "")).
		Register(view.NewModule(), probe).
		Use(probe.middleware())
	if err := a.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}

	handler := a.Handler()
	for _, path := range []string{
		"/probe",
		"/_arandu/health",
		"/_arandu/assets/000000000000/app.css",
		"/_aranduish/probe",
	} {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}

	want := []string{"/probe", "/_aranduish/probe"}
	if len(probe.seen) != len(want) {
		t.Fatalf("the application's middleware saw %v, want %v: what the pipeline covers changed", probe.seen, want)
	}
	for i := range want {
		if probe.seen[i] != want[i] {
			t.Errorf("request %d reached the middleware as %q, want %q", i, probe.seen[i], want[i])
		}
	}
}
