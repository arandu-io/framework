package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/framework/validation"
)

// signInScreen builds the part of the module the sign-in screen needs: a CSRF
// issuer and a session store. Nothing here reaches the database, so no fake one
// is wired -- a test that needed one would be testing something else.
func signInScreen() *Module {
	key := []byte("0123456789abcdef0123456789abcdef")
	return &Module{svc: &Service{
		session: security.NewSessionStore(key, time.Hour, false, security.NewMemoryBackend()),
		csrf:    security.NewCSRF(key, time.Hour),
	}}
}

// A wrong password is the most common refusal this framework answers, and it
// used to reach nobody.
//
// The answer was a bare <div class="alert"> with no document around it. To a
// browser that IS the whole page: an error on a blank background, with no form
// to type into and no way back except the back button. To htmx it was nothing at
// all -- htmx swaps no 4xx, so the screen stayed exactly as it was and the
// person saw a button that did nothing.
func TestAWrongPasswordIsAnsweredWithTheSignInScreenAndNotABareFragment(t *testing.T) {
	rec := httptest.NewRecorder()
	signInScreen().renderLogin(rec, httptest.NewRequest(http.MethodPost, "/auth/login", nil),
		http.StatusUnauthorized, "someone@example.com",
		validation.Errors{"email": {"invalid email or password"}})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 -- the status is what the logs read, and it does not change", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		// A page, not a fragment: there is somewhere to type again.
		"<form", `name="password"`, `name="_csrf"`,
		// And it says what went wrong.
		"invalid email or password",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the refusal leaves the person with no %s:\n%s", want, body)
		}
	}
}

// The address is put back. A sign-in screen that clears it makes somebody type
// it again to discover that it was the password that was wrong.
func TestARefusedSignInKeepsTheAddressThatWasTyped(t *testing.T) {
	rec := httptest.NewRecorder()
	signInScreen().renderLogin(rec, httptest.NewRequest(http.MethodPost, "/auth/login", nil),
		http.StatusUnauthorized, "someone@example.com",
		validation.Errors{"email": {"invalid email or password"}})

	if !strings.Contains(rec.Body.String(), `value="someone@example.com"`) {
		t.Errorf("the address was cleared, so they have to type it again to find out the password was the problem:\n%s", rec.Body.String())
	}
}

// The form posts natively rather than through hx-boost, and that is the whole
// reason the refusal above is visible: htmx's response handling is
// `{code:"[45]..", swap:false, error:true}`, so a boosted post leaves every 4xx
// on the floor. The guards' answer -- HX-Refresh -- cannot be used here, because
// reloading the sign-in screen throws away both the message and the address.
func TestTheSignInFormPostsInAWayThatCanShowARefusal(t *testing.T) {
	rec := httptest.NewRecorder()
	signInScreen().renderLogin(rec, httptest.NewRequest(http.MethodGet, "/auth/login", nil), http.StatusOK, "", nil)

	body := rec.Body.String()
	if !strings.Contains(body, `<form method="post" action="/auth/login" hx-boost="false"`) {
		t.Error("the form is boosted, so htmx discards every refusal it answers and the screen does not change")
	}
	// The token still has to travel: the native post carries the hidden field,
	// not the hx-headers attribute.
	if !strings.Contains(body, `<input type="hidden" name="_csrf"`) {
		t.Error("the form posts natively and carries no _csrf field, so every sign-in fails the CSRF check")
	}
}

// The first visit is a plain page with no error box on it. A screen that greets
// everybody with "that did not work" teaches people to ignore the box.
func TestTheFirstVisitToTheSignInScreenShowsNoRefusal(t *testing.T) {
	rec := httptest.NewRecorder()
	signInScreen().renderLogin(rec, httptest.NewRequest(http.MethodGet, "/auth/login", nil), http.StatusOK, "", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "That did not work") {
		t.Error("somebody who has not tried anything yet is told it did not work")
	}
}
