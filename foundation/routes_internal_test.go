package foundation

import (
	"context"
	"fmt"
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
//
// Which is why each line written down carries the owning module rather than the
// test requiring one. It used to refuse any route under the prefix that was not
// tagged "arandu", and nothing under the prefix owes that tag: the asset route
// belongs to the view module and says so, and calling it "arandu" would name the
// wrong owner. The rule held only because an Application with no modules
// registered has no route but its own to check. Written into the expectation, a
// wrong tag still fails and a second owner is a line to add.
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
			want: []string{
				internalPrefix + "health [arandu]",
				internalPrefix + "live [arandu]",
			},
		},
		{
			name:   "production with a tracing secret",
			env:    config.EnvProd,
			secret: "secret",
			want: []string{
				internalPrefix + "health [arandu]",
				internalPrefix + "live [arandu]",
				observability.ConsolePath + " [arandu]",
				observability.ConsolePath + "/{id} [arandu]",
			},
		},
		{
			name: "development",
			env:  config.EnvDev,
			want: []string{
				internalPrefix + "health [arandu]",
				internalPrefix + "live [arandu]",
				reloadPath + " [arandu]",
				observability.ConsolePath + " [arandu]",
				observability.ConsolePath + "/{id} [arandu]",
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
				got = append(got, fmt.Sprintf("%s [%s]", r.Pattern, r.Module))
			}

			if len(got) != len(tc.want) {
				t.Fatalf("internal routes = %v, want %v: the surface the application's middleware does not cover changed", got, tc.want)
			}
			for i, want := range tc.want {
				if got[i] != want {
					t.Errorf("internal route %d = %q, want %q", i, got[i], want)
				}
			}
		})
	}
}
