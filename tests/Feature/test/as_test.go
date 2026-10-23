package feature

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arandu-io/framework/test"
	"github.com/arandu-io/hesape/auth"
	hhttp "github.com/arandu-io/hesape/http"
	"github.com/arandu-io/hesape/http/middleware"
	"github.com/arandu-io/hesape/routing"
)

// accounts is where the application below keeps who has which role: what a
// real application keeps in its users table, read by its resolver on every
// request.
type accounts struct {
	mu    sync.Mutex
	roles map[string][]string
	reads atomic.Int32
}

func (a *accounts) set(id string, roles ...string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.roles[id] = roles
}

func (a *accounts) resolve(_ context.Context, who auth.Subject) (auth.Subject, error) {
	a.reads.Add(1)
	a.mu.Lock()
	defer a.mu.Unlock()
	roles, ok := a.roles[who.ID]
	if !ok || who.Tenant != "acme" {
		return auth.Subject{}, hhttp.ErrSubjectGone
	}
	return auth.Subject{ID: who.ID, Tenant: who.Tenant, Roles: append([]string(nil), roles...)}, nil
}

// guarded serves an application whose /admin is behind RequireRole and whose
// /me is behind RequireAuth, both answering what auth.SubjectFrom reads.
func guarded(t *testing.T) (*test.Server, *accounts) {
	t.Helper()
	accts := &accounts{roles: map[string][]string{}}
	sessions := hhttp.NewSessionStore([]byte("0123456789abcdef0123456789abcdef"), time.Hour, false, nil)
	sessions.ResolveSubjects(accts.resolve)

	whoami := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, ok := auth.SubjectFrom(r.Context())
		if !ok {
			_, _ = w.Write([]byte("nobody"))
			return
		}
		_, _ = w.Write([]byte(s.ID + " of " + s.Tenant + " as [" + strings.Join(s.Roles, ",") + "]"))
	})
	r := routing.NewRouter()
	r.Get("/me", middleware.RequireAuth(sessions)(whoami)).Name("me")
	r.Get("/admin", middleware.RequireRole(sessions, "admin")(whoami)).Name("admin")

	server := test.Serve(t, func(context.Context, test.Env) (test.Application, error) {
		return test.Application{Handler: r, Routes: r.Routes(), Sessions: sessions}, nil
	}, test.SQLite(), test.Migrate(noMigration))
	return server, accts
}

// As reaches the handler through the guard: RequireAuth loads the session,
// the resolver answers the roles, and auth.SubjectFrom reads what it
// answered. A client that never signed in is sent to the sign-in screen.
func TestAsPassesTheGuardThroughTheSessionAndTheResolver(t *testing.T) {
	server, accts := guarded(t)
	accts.set("u1", "admin")

	server.Client().Get("/me").AssertStatus(http.StatusSeeOther).AssertRedirect(middleware.SignInPath)

	before := accts.reads.Load()
	alice := test.As(t, server, auth.Subject{ID: "u1", Tenant: "acme"})
	alice.Get("/me").AssertOk().AssertSee("u1 of acme as [admin]")
	alice.Get("/admin").AssertOk()

	if accts.reads.Load()-before < 3 {
		t.Errorf("the resolver was read %d times for a sign-in and two requests, want one read each",
			accts.reads.Load()-before)
	}
}

// The role is read from the account on every request, never kept from the
// sign-in: a demotion takes effect on the next request of a session that
// already exists.
func TestADemotedRoleLosesAccessOnTheNextRequest(t *testing.T) {
	server, accts := guarded(t)
	accts.set("u1", "admin")
	alice := test.As(t, server, auth.Subject{ID: "u1", Tenant: "acme"})
	alice.Get("/admin").AssertOk()

	accts.set("u1", "member")

	alice.Get("/admin").AssertStatus(http.StatusForbidden)
	alice.Get("/me").AssertOk().AssertSee("u1 of acme as [member]")
}

// The session belongs to the application, so the cookies As hands back sign a
// browser in over the socket as well: what an end-to-end journey is given.
func TestAsCookiesSignInOverTheSocket(t *testing.T) {
	server, accts := guarded(t)
	accts.set("u1", "admin")
	cookies := test.As(t, server, auth.Subject{ID: "u1", Tenant: "acme"}).Cookies()
	if len(cookies) == 0 {
		t.Fatal("As handed back no cookie")
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+server.Route("admin"), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /admin over the socket with the session answered %d, want 200", resp.StatusCode)
	}
}

// An application whose store has no resolver keeps the subject it was given,
// and As works for it as long as the subject decides nothing.
func TestAsSignsInWithoutAResolver(t *testing.T) {
	sessions := hhttp.NewSessionStore([]byte("0123456789abcdef0123456789abcdef"), time.Hour, false, nil)
	r := routing.NewRouter()
	r.Get("/me", middleware.RequireAuth(sessions)(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		s, _ := auth.SubjectFrom(req.Context())
		_, _ = w.Write([]byte(s.ID + " of " + s.Tenant))
	})))
	server := test.Serve(t, func(context.Context, test.Env) (test.Application, error) {
		return test.Application{Handler: r, Sessions: sessions}, nil
	}, test.SQLite(), test.Migrate(noMigration))

	test.As(t, server, auth.Subject{ID: "u2", Tenant: "acme"}).Get("/me").AssertOk().AssertSee("u2 of acme")
}
