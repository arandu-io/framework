package feature

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/http/middleware"
	"github.com/arandu-io/framework/security"
)

// seen is what a controller action behind a guard read from ctx.User.
type seen struct {
	reached bool
	subject security.Subject
	ok      bool
}

// behind registers one controller action behind the guard, on a real router, and
// returns what that action saw. The action reads ctx.User and nothing else: the
// claim under test is that a handler needs no session store to know who asked.
func behind(guard func(http.Handler) http.Handler) (http.Handler, *seen) {
	got := &seen{}
	r := fhttp.NewRouter()
	r.Action(http.MethodGet, "/dashboard", func(ctx *fhttp.Context) error {
		got.reached = true
		got.subject, got.ok = ctx.User()
		return nil
	}, guard)
	return r, got
}

func TestAHandlerBehindRequireAuthSeesTheSessionSubject(t *testing.T) {
	sessions := newSessions()
	h, got := behind(middleware.RequireAuth(sessions))

	want := security.Subject{ID: "u-1", Tenant: "acme", Roles: []string{"admin"}}
	h.ServeHTTP(httptest.NewRecorder(), signedIn(t, sessions, want, http.MethodGet, "/dashboard"))

	if !got.reached {
		t.Fatal("the action behind the guard never ran")
	}
	if !got.ok {
		t.Fatal("ctx.User reported no subject behind RequireAuth, so every handler has to load the session again")
	}
	if got.subject.ID != want.ID || got.subject.Tenant != want.Tenant || !got.subject.HasRole("admin") {
		t.Errorf("ctx.User = %+v, want the session's subject %+v", got.subject, want)
	}
}

// The role guard and the confirmation guard load the same session RequireAuth
// does, and a handler mounted behind either of them is no less signed in.
func TestEveryGuardThatLoadsTheSessionCarriesTheSubject(t *testing.T) {
	want := security.Subject{ID: "u-2", Tenant: "acme", Roles: []string{"admin"}}

	t.Run("RequireRole", func(t *testing.T) {
		sessions := newSessions()
		h, got := behind(middleware.RequireRole(sessions, "admin"))
		h.ServeHTTP(httptest.NewRecorder(), signedIn(t, sessions, want, http.MethodGet, "/dashboard"))
		if !got.ok || got.subject.ID != want.ID {
			t.Errorf("ctx.User = %+v, %v behind RequireRole, want %q", got.subject, got.ok, want.ID)
		}
	})

	t.Run("RequireConfirmedPassword", func(t *testing.T) {
		sessions := newSessions()
		h, got := behind(middleware.RequireConfirmedPassword(sessions))
		r := confirmed(t, sessions, signedIn(t, sessions, want, http.MethodGet, "/dashboard"))
		h.ServeHTTP(httptest.NewRecorder(), r)
		if !got.ok || got.subject.ID != want.ID {
			t.Errorf("ctx.User = %+v, %v behind RequireConfirmedPassword, want %q", got.subject, got.ok, want.ID)
		}
	})
}

func TestLoadSubjectCarriesTheSubjectOfASignedInRequest(t *testing.T) {
	sessions := newSessions()
	h, got := behind(middleware.LoadSubject(sessions))

	want := security.Subject{ID: "u-1", Tenant: "acme"}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, signedIn(t, sessions, want, http.MethodGet, "/dashboard"))

	if !got.reached {
		t.Fatalf("LoadSubject stopped a signed-in request: %d", rec.Code)
	}
	if !got.ok || got.subject.ID != want.ID || got.subject.Tenant != want.Tenant {
		t.Errorf("ctx.User = %+v, %v, want the session's subject %+v", got.subject, got.ok, want)
	}
}

// A guest is let through untouched: no redirect, and no subject -- not even an
// anonymous one, because "nobody loaded a session" and "a declared guest" are
// different facts and only the code that means the second may declare it.
func TestLoadSubjectLetsAGuestThroughWithNoSubject(t *testing.T) {
	h, got := behind(middleware.LoadSubject(newSessions()))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard", nil))

	if !got.reached {
		t.Fatalf("LoadSubject turned a guest away from a public page: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if got.ok {
		t.Errorf("ctx.User reported %+v for a request with no session", got.subject)
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Errorf("a guest was redirected to %q", loc)
	}
}

// A cookie whose session is gone is a guest to LoadSubject and a redirect to
// RequireAuth, exactly as before the subject was carried.
func TestAnExpiredSessionCarriesNoSubject(t *testing.T) {
	sessions := newSessions()
	expired := func() *http.Request {
		r := signedIn(t, sessions, security.Subject{ID: "u-1", Tenant: "acme"}, http.MethodGet, "/dashboard")
		if err := sessions.Destroy(context.Background(), httptest.NewRecorder(), sessions.IDFromRequest(r)); err != nil {
			t.Fatalf("destroying the session: %v", err)
		}
		return r
	}

	t.Run("RequireAuth", func(t *testing.T) {
		h, got := behind(middleware.RequireAuth(sessions))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, expired())
		if got.reached {
			t.Fatal("an expired session reached the action behind RequireAuth")
		}
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != middleware.SignInPath {
			t.Errorf("answer = %d to %q, want 303 to %q", rec.Code, rec.Header().Get("Location"), middleware.SignInPath)
		}
	})

	t.Run("LoadSubject", func(t *testing.T) {
		h, got := behind(middleware.LoadSubject(sessions))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, expired())
		if !got.reached {
			t.Fatalf("LoadSubject stopped a request whose session expired: %d", rec.Code)
		}
		if got.ok {
			t.Errorf("ctx.User reported %+v for a session that no longer exists", got.subject)
		}
	})
}
