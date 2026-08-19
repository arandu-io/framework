package framework_test

import (
	"context"
	"fmt"
	"log/slog"
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
