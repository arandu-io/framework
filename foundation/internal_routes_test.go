package foundation

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/arandu-io/framework/foundation/bootstrap"
	"github.com/arandu-io/framework/observability"
	"github.com/arandu-io/hesape/config"
	"github.com/arandu-io/hesape/encryption"
)

// internalCfg is the configuration the test below reads and nothing else. It
// opens no connection and binds no port.
func internalCfg(env config.Env, secret string) bootstrap.Configuration {
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

// TestTheApplicationsInternalSurfaceIsInTheInventory writes down what the
// Application mounts under internalPrefix, in the one place the list can be read.
//
// The prefix answers to this framework's policy instead of the application's, so
// what sits under it is a decision rather than a detail: an endpoint added here
// is a path no rate limit, no CSRF check and no security header of the
// application covers. This is what makes the addition deliberate -- a new
// internal route fails the test until somebody writes it down, and writing it
// down is the moment to ask what gates it.
//
// It also holds the other half of the claim: each one is a registered route
// carrying the module that owns it, so the route table is the inventory rather
// than most of it.
//
// The Application is not the only owner of the prefix, which is why the name says
// whose surface this is. The view module registers the content-addressed asset
// route under the same prefix, from a prefix constant of its own, and it is not
// visible from here -- that package imports this one in order to be a module. The
// paragraph on internalPrefix carries what the second owner costs.
func TestTheApplicationsInternalSurfaceIsInTheInventory(t *testing.T) {
	for _, tc := range []struct {
		name   string
		env    config.Env
		secret string
		want   []string
	}{
		{
			name: "production without a tracing secret",
			env:  config.EnvProd,
			want: []string{internalPrefix + "health"},
		},
		{
			name:   "production with a tracing secret",
			env:    config.EnvProd,
			secret: "secret",
			want: []string{
				internalPrefix + "health",
				observability.ConsolePath,
				observability.ConsolePath + "/{id}",
			},
		},
		{
			name: "development",
			env:  config.EnvDev,
			want: []string{
				internalPrefix + "health",
				reloadPath,
				observability.ConsolePath,
				observability.ConsolePath + "/{id}",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := New(internalCfg(tc.env, tc.secret))
			if err := a.Boot(context.Background()); err != nil {
				t.Fatalf("Boot: %v", err)
			}

			var got []string
			for _, r := range a.Routes() {
				if !strings.HasPrefix(r.Pattern, internalPrefix) {
					continue
				}
				if r.Module != "arandu" {
					t.Errorf("%s %s is tagged with module %q: an internal route outside this framework's own module is invisible to whoever groups the table by owner",
						r.Method, r.Pattern, r.Module)
				}
				got = append(got, r.Pattern)
			}

			if len(got) != len(tc.want) {
				t.Fatalf("internal routes = %v, want %v: the surface the application's middleware does not cover changed", got, tc.want)
			}
			for i, pattern := range tc.want {
				if got[i] != pattern {
					t.Errorf("internal route %d = %q, want %q", i, got[i], pattern)
				}
			}
		})
	}
}
