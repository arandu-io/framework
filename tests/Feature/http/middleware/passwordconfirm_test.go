package feature

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/arandu-io/framework/http/middleware"
	"github.com/arandu-io/framework/security"
)

// confirmed stamps the request's session as having had its password typed again
// just now, through the store rather than by writing the field: what the guard
// reads is what SessionStore.Confirm writes, and a test that set the field by
// hand would keep passing after the two stopped agreeing.
func confirmed(t *testing.T, s *security.SessionStore, r *http.Request) *http.Request {
	t.Helper()
	if err := s.Confirm(context.Background(), httptest.NewRecorder(), r); err != nil {
		t.Fatalf("recording the password confirmation: %v", err)
	}
	return r
}

func TestASessionThatNeverConfirmedItsPasswordIsSentToTheConfirmationScreen(t *testing.T) {
	sessions := newSessions()
	h := guarded(middleware.RequireConfirmedPassword(sessions))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, signedIn(t, sessions, security.Subject{ID: "u-1", Tenant: "acme"}, http.MethodGet, "/account/api-keys"))

	if rec.Body.String() == "reached" {
		t.Fatal("a page behind the confirmation guard rendered for a session that has never proved anybody is holding it: " +
			"a cookie lifted from a shared machine is a takeover with no step the attacker has to know anything for")
	}
	if got := rec.Header().Get("Location"); got != middleware.PasswordConfirmPath {
		t.Errorf("they were sent to %q, and the confirmation screen is at %q", got, middleware.PasswordConfirmPath)
	}
}

func TestASessionThatJustConfirmedItsPasswordReachesTheSensitivePage(t *testing.T) {
	sessions := newSessions()
	h := guarded(middleware.RequireConfirmedPassword(sessions))

	r := confirmed(t, sessions, signedIn(t, sessions,
		security.Subject{ID: "u-1", Tenant: "acme"}, http.MethodGet, "/account/api-keys"))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Body.String() != "reached" {
		t.Fatalf("somebody who has just typed their password was asked for it again: %d %q -- a screen that asks "+
			"twice in one minute is a screen people learn to click through", rec.Code, rec.Body.String())
	}
}

// Nobody signed in has a password to confirm, so they are sent to sign in first.
// Sending them to the confirmation form instead is a loop: the form posts, the
// handler finds no session, and it sends them back to the form.
func TestSomebodyWithNoSessionIsSentToSignInRatherThanToConfirmAPassword(t *testing.T) {
	h := guarded(middleware.RequireConfirmedPassword(newSessions()))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/account/api-keys", nil))

	if got := rec.Header().Get("Location"); got != middleware.SignInPath {
		t.Errorf("a visitor with no session was sent to %q, want the sign-in screen at %q", got, middleware.SignInPath)
	}
}

// The confirmation is a fact about the session, not about the subject: opening a
// second session must not inherit what the first one proved, or signing in on a
// borrowed laptop hands that laptop everything the original session confirmed.
func TestAFreshSessionDoesNotInheritTheConfirmationAnotherOneEarned(t *testing.T) {
	sessions := newSessions()
	h := guarded(middleware.RequireConfirmedPassword(sessions))
	subject := security.Subject{ID: "u-1", Tenant: "acme"}

	confirmed(t, sessions, signedIn(t, sessions, subject, http.MethodGet, "/account/api-keys"))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, signedIn(t, sessions, subject, http.MethodGet, "/account/api-keys"))

	if rec.Body.String() == "reached" {
		t.Fatal("a session opened after another one confirmed the password was let straight through")
	}
}

// The guard keeps where the person was going, so the confirmation screen can
// finish the journey. Without it every confirmation ends on the front page, and
// somebody who clicked "reveal my API key" has to find that screen again.
func TestTheGuardKeepsWhereTheyWereGoing(t *testing.T) {
	sessions := newSessions()
	h := guarded(middleware.RequireConfirmedPassword(sessions))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, signedIn(t, sessions, security.Subject{ID: "u-1", Tenant: "acme"}, http.MethodGet, "/account/api-keys"))

	var kept bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == security.IntendedCookieName && c.Value != "" {
			kept = true
		}
	}
	if !kept {
		t.Error("the address behind the guard was not remembered, so confirming a password drops the person on the front page")
	}
}

// A guard's answer is about who is asking, so it must never be stored by a cache
// shared between people: served to somebody else, "go and confirm your password"
// is a redirect they cannot satisfy.
func TestTheConfirmationGuardsAnswerIsNotCacheable(t *testing.T) {
	sessions := newSessions()
	h := guarded(middleware.RequireConfirmedPassword(sessions))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, signedIn(t, sessions, security.Subject{ID: "u-1", Tenant: "acme"}, http.MethodGet, "/account/api-keys"))

	if got := rec.Header().Get("Cache-Control"); got == "" {
		t.Error("the guard's redirect carries no Cache-Control, so a shared proxy may serve one person's answer to another")
	}
}
