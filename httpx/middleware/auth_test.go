package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/framework/httpx"
	"github.com/arandu-io/framework/httpx/middleware"
	"github.com/arandu-io/framework/security"
)

// guarded returns a store, and a handler behind the guard that writes "reached"
// when the guard let the request through.
func guarded(guard func(http.Handler) http.Handler) http.Handler {
	reached := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("reached"))
	})
	return httpx.Chain(reached, guard)
}

func newSessions() *security.SessionStore {
	return security.NewSessionStore(appKey, time.Hour, false, security.NewMemoryBackend())
}

// signedIn returns a request carrying a real session cookie for the subject,
// issued by the store the guard will read it with. Forging the cookie by hand
// would prove the test can write base64, not that the guard reads a session.
func signedIn(t *testing.T, s *security.SessionStore, sub security.Subject, method, path string) *http.Request {
	t.Helper()

	rec := httptest.NewRecorder()
	if _, err := s.Start(context.Background(), rec, sub); err != nil {
		t.Fatalf("opening a session for the test: %v", err)
	}

	r := httptest.NewRequest(method, path, nil)
	for _, c := range rec.Result().Cookies() {
		r.AddCookie(c)
	}
	return r
}

func TestSomebodyWithNoSessionIsSentToTheSignInScreen(t *testing.T) {
	h := guarded(middleware.RequireAuth(newSessions()))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard", nil))

	if rec.Body.String() == "reached" {
		t.Fatal("the dashboard rendered for a visitor with no session")
	}
	if rec.Code != http.StatusSeeOther {
		t.Errorf("a visitor with no session got %d, want 303 to the sign-in screen", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != middleware.SignInPath {
		t.Errorf("they were sent to %q, and the sign-in screen is at %q", got, middleware.SignInPath)
	}
}

func TestSomebodySignedInReachesThePageBehindTheGuard(t *testing.T) {
	sessions := newSessions()
	h := guarded(middleware.RequireAuth(sessions))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, signedIn(t, sessions, security.Subject{ID: "u-1", Tenant: "acme"}, http.MethodGet, "/dashboard"))

	if rec.Body.String() != "reached" {
		t.Fatalf("a signed-in person was refused their own dashboard: %d %q", rec.Code, rec.Body.String())
	}
}

// A session the backend no longer holds is the same situation as no session at
// all, and it is the common one: the cookie is still in the browser after the
// session expired or somebody signed out elsewhere.
func TestAnExpiredSessionIsTreatedAsNoSession(t *testing.T) {
	sessions := newSessions()
	h := guarded(middleware.RequireAuth(sessions))

	r := signedIn(t, sessions, security.Subject{ID: "u-1", Tenant: "acme"}, http.MethodGet, "/dashboard")
	if err := sessions.Destroy(context.Background(), httptest.NewRecorder(), sessions.IDFromRequest(r)); err != nil {
		t.Fatalf("destroying the session: %v", err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Body.String() == "reached" {
		t.Fatal("a cookie whose session is gone still opened the page")
	}
	if rec.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303 to the sign-in screen", rec.Code)
	}
}

func TestTheGuestGuardSendsASignedInPersonAwayFromTheSignInScreen(t *testing.T) {
	sessions := newSessions()
	h := guarded(middleware.RedirectIfAuthenticated(sessions, "/"))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, signedIn(t, sessions, security.Subject{ID: "u-1", Tenant: "acme"}, http.MethodGet, middleware.SignInPath))

	if rec.Body.String() == "reached" {
		t.Fatal("the sign-in screen rendered for somebody who is already signed in, which reads to them as having been signed out")
	}
	if rec.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303 away from the sign-in screen", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/" {
		t.Errorf("they were sent to %q, want / -- the address the guard was given", got)
	}
}

func TestTheGuestGuardLetsAGuestReachTheSignInScreen(t *testing.T) {
	h := guarded(middleware.RedirectIfAuthenticated(newSessions(), "/"))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, middleware.SignInPath, nil))

	if rec.Body.String() != "reached" {
		t.Fatalf("nobody can sign in: the guest guard turned the sign-in screen away for a visitor with no session (%d)", rec.Code)
	}
}

func TestARoleGuardRefusesSomebodyWithoutTheRole(t *testing.T) {
	sessions := newSessions()
	h := guarded(middleware.RequireRole(sessions, "admin"))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, signedIn(t, sessions,
		security.Subject{ID: "u-1", Tenant: "acme", Roles: []string{"reader"}}, http.MethodGet, "/admin/"))

	if rec.Body.String() == "reached" {
		t.Fatal("a reader walked into the moderation area")
	}
	// 403 and not a redirect: they are signed in, so the sign-in screen has
	// nothing to offer them, and not 404 either -- the page exists.
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestARoleGuardAdmitsSomebodyCarryingAnyOfTheRoles(t *testing.T) {
	sessions := newSessions()
	h := guarded(middleware.RequireRole(sessions, "owner", "admin"))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, signedIn(t, sessions,
		security.Subject{ID: "u-1", Tenant: "acme", Roles: []string{"admin"}}, http.MethodGet, "/admin/"))

	if rec.Body.String() != "reached" {
		t.Fatalf("an administrator was refused the moderation area: %d %q", rec.Code, rec.Body.String())
	}
}

// Somebody who is not signed in at all meets the sign-in screen, not a 403: the
// role is not the reason they were turned away and telling them it is sends
// them to ask for a permission they already have.
func TestARoleGuardSendsAVisitorWithNoSessionToSignInFirst(t *testing.T) {
	h := guarded(middleware.RequireRole(newSessions(), "admin"))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/", nil))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 to the sign-in screen", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != middleware.SignInPath {
		t.Errorf("they were sent to %q, want %q", got, middleware.SignInPath)
	}
}

// A 303 answered to an HTMX request is followed inside the fragment, so the
// sign-in screen ends up swapped into a corner of the page it was meant to
// replace. HX-Redirect with 204 is the answer that client acts on.
func TestAnHtmxRequestIsToldToNavigateRatherThanSwapTheSignInScreenIn(t *testing.T) {
	for name, guard := range map[string]func(http.Handler) http.Handler{
		"RequireAuth": middleware.RequireAuth(newSessions()),
		"RequireRole": middleware.RequireRole(newSessions(), "admin"),
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
			r.Header.Set("HX-Request", "true")

			rec := httptest.NewRecorder()
			guarded(guard).ServeHTTP(rec, r)

			if got := rec.Header().Get("HX-Redirect"); got != middleware.SignInPath {
				t.Errorf("HX-Redirect = %q, want %q: without it HTMX swaps the whole sign-in page into the fragment", got, middleware.SignInPath)
			}
			if rec.Code != http.StatusNoContent {
				t.Errorf("status = %d, want 204 -- a body alongside HX-Redirect is swapped in before the browser navigates", rec.Code)
			}
			if rec.Body.Len() != 0 {
				t.Errorf("the answer carries a body: %q", rec.Body.String())
			}
		})
	}
}

// The guest guard answers the same two shapes, for the same reason: the person
// who submitted the sign-in form from a page that is already signed in should
// end up on the dashboard, not with the dashboard inside the form.
func TestTheGuestGuardAnswersHtmxTheSameWay(t *testing.T) {
	sessions := newSessions()

	r := signedIn(t, sessions, security.Subject{ID: "u-1", Tenant: "acme"}, http.MethodGet, middleware.SignInPath)
	r.Header.Set("HX-Request", "true")

	rec := httptest.NewRecorder()
	guarded(middleware.RedirectIfAuthenticated(sessions, "/dashboard")).ServeHTTP(rec, r)

	if got := rec.Header().Get("HX-Redirect"); got != "/dashboard" {
		t.Errorf("HX-Redirect = %q, want /dashboard", got)
	}
	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
}

// A guard's answer is about who is asking, so no cache shared between people may
// keep it. The HTMX answer is the dangerous one: 204 is heuristically cacheable,
// and a proxy holding the guest guard's "you are already signed in, go to /"
// serves it to a guest -- who is then bounced off the sign-in screen and cannot
// sign in at all.
func TestAGuardsAnswerIsNeverStoredByACacheSharedBetweenPeople(t *testing.T) {
	sessions := newSessions()

	htmx := func(r *http.Request) *http.Request {
		r.Header.Set("HX-Request", "true")
		return r
	}

	for name, c := range map[string]struct {
		guard   func(http.Handler) http.Handler
		request *http.Request
	}{
		"RequireAuth turning a visitor away": {
			middleware.RequireAuth(sessions),
			htmx(httptest.NewRequest(http.MethodGet, "/dashboard", nil)),
		},
		"the guest guard turning a signed-in person away": {
			middleware.RedirectIfAuthenticated(sessions, "/"),
			htmx(signedIn(t, sessions, security.Subject{ID: "u-1", Tenant: "acme"}, http.MethodGet, middleware.SignInPath)),
		},
	} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			guarded(c.guard).ServeHTTP(rec, c.request)

			if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") {
				t.Errorf("Cache-Control = %q, and a shared cache is free to replay this answer at somebody it was not about", got)
			}
		})
	}
}

// The guard is the only thing that knows where somebody was going: by the time
// they have typed a password, the request that was refused is gone. Without
// this, every sign-in lands on the front page and whoever followed a link to one
// invoice has to go and find it again.
func TestSomebodyTurnedAwayIsSentBackToThePageTheyWereRefused(t *testing.T) {
	for name, guard := range map[string]func(*security.SessionStore) func(http.Handler) http.Handler{
		"RequireAuth": middleware.RequireAuth,
		"RequireRole": func(s *security.SessionStore) func(http.Handler) http.Handler {
			return middleware.RequireRole(s, "admin")
		},
	} {
		t.Run(name, func(t *testing.T) {
			sessions := newSessions()

			refused := httptest.NewRecorder()
			guarded(guard(sessions)).ServeHTTP(refused, httptest.NewRequest(http.MethodGet, "/invoices/42?tab=lines", nil))

			// The sign-in screen, reached with whatever the guard left in the
			// browser.
			signIn := httptest.NewRequest(http.MethodGet, middleware.SignInPath, nil)
			for _, c := range refused.Result().Cookies() {
				signIn.AddCookie(c)
			}

			if got := sessions.TakeIntended(httptest.NewRecorder(), signIn, "/"); got != "/invoices/42?tab=lines" {
				t.Fatalf("after signing in they land on %q, and they had asked for /invoices/42?tab=lines", got)
			}
		})
	}
}

// A guard mounted broadly enough to cover the sign-in screen itself would send
// somebody back to the form they have just filled in, which reads to them as the
// password having been wrong.
func TestNobodyIsSentBackToTheSignInScreenTheyJustCameFrom(t *testing.T) {
	sessions := newSessions()

	rec := httptest.NewRecorder()
	guarded(middleware.RequireAuth(sessions)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, middleware.SignInPath, nil))

	signIn := httptest.NewRequest(http.MethodGet, middleware.SignInPath, nil)
	for _, c := range rec.Result().Cookies() {
		signIn.AddCookie(c)
	}
	if got := sessions.TakeIntended(httptest.NewRecorder(), signIn, "/"); got != "/" {
		t.Fatalf("signing in would send them to %q, which is the screen they just signed in on", got)
	}
}

// htmx swaps neither a 403 nor a 419 -- its response handling is
// `{code:"[45]..", swap:false, error:true}` -- so a refusal used to reach a
// person as nothing at all: they pressed the button and the screen did not
// change, with the sentence explaining why in a body that was thrown away.
//
// HX-Refresh makes the browser reload the page as an ordinary navigation, where
// the same middleware answers the same refusal to a request htmx is not
// handling, and the browser renders it. Both refusals do it, because doing it in
// one of them would be the inconsistency rather than the fix.
func TestARefusedHtmxRequestIsAnsweredInAShapeThePersonCanSee(t *testing.T) {
	sessions := newSessions()

	r := signedIn(t, sessions,
		security.Subject{ID: "u-1", Tenant: "acme", Roles: []string{"reader"}}, http.MethodGet, "/admin/")
	r.Header.Set("HX-Request", "true")

	rec := httptest.NewRecorder()
	guarded(middleware.RequireRole(sessions, "admin")).ServeHTTP(rec, r)

	if rec.Header().Get("HX-Refresh") != "true" {
		t.Error("htmx was given nothing to do with this refusal, so the person clicked and nothing happened at all")
	}
	// The status is unchanged: what a log, a probe and every non-HTMX client
	// see is exactly what they saw before.
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

// The other half: a browser that is not running htmx already renders the body,
// and must not be told to reload -- a reload would replace the sentence with the
// page the person was on and explain nothing.
func TestAPlainBrowserIsNotToldToReloadOverARefusal(t *testing.T) {
	sessions := newSessions()

	rec := httptest.NewRecorder()
	guarded(middleware.RequireRole(sessions, "admin")).ServeHTTP(rec, signedIn(t, sessions,
		security.Subject{ID: "u-1", Tenant: "acme", Roles: []string{"reader"}}, http.MethodGet, "/admin/"))

	if got := rec.Header().Get("HX-Refresh"); got != "" {
		t.Errorf("HX-Refresh = %q on a request no htmx sent", got)
	}
	if !strings.Contains(rec.Body.String(), "not open to your account") {
		t.Errorf("the refusal says nothing a person can read: %q", rec.Body.String())
	}
}

// RULE 9, made a compile-and-boot problem rather than a review comment:
// RequireRole with nothing to require is RequireAuth under a second name, and a
// route wired that way guards nothing at all.
func TestARoleGuardWithNoRoleIsRefusedAtWiringTime(t *testing.T) {
	defer func() {
		got, ok := recover().(string)
		if !ok {
			t.Fatal("RequireRole with no role was accepted: the route it guards admits everybody with a session, which is RequireAuth wearing another name")
		}
		if !strings.Contains(got, "RequireAuth") {
			t.Errorf("the panic does not name what to use instead: %q", got)
		}
	}()

	middleware.RequireRole(newSessions())
}
