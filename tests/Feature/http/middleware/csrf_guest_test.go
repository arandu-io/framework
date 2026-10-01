package feature

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/http/middleware"
	"github.com/arandu-io/framework/security"
	hhttp "github.com/arandu-io/hesape/http"
	"github.com/arandu-io/hesape/session"
)

// issuing runs CSRFProtect over csrf with a request that carries no session,
// and returns what the handler found on the context and what the response set.
func issuing(t *testing.T, csrf *security.CSRF, method string) (token string, found bool, guest *http.Cookie) {
	t.Helper()
	h := fhttp.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, found = hhttp.CSRFTokenFrom(r.Context())
	}), middleware.CSRFProtect(csrf, func(*http.Request) string { return "" }))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, "/signin", nil))
	for _, c := range rec.Result().Cookies() {
		if c.Name == session.GuestCookieName {
			guest = c
		}
	}
	return token, found, guest
}

// A page a guest loads carries a token, and the guest cookie it is bound to is
// one script cannot read and a browser sends only over HTTPS.
func TestAGuestPageIsIssuedATokenAndASecureGuestCookie(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		token, found, guest := issuing(t, security.NewCSRF(appKey, time.Hour), method)
		if !found || token == "" {
			t.Errorf("%s: no token on the request context, so view.New draws a form that is refused", method)
		}
		if guest == nil {
			t.Fatalf("%s: no %s cookie was set, so the token is bound to nothing", method, session.GuestCookieName)
		}
		if !guest.HttpOnly || !guest.Secure || guest.SameSite != http.SameSiteLaxMode {
			t.Errorf("%s: guest cookie HttpOnly=%v Secure=%v SameSite=%v, want true, true, Lax",
				method, guest.HttpOnly, guest.Secure, guest.SameSite)
		}
	}
}

// Development over plain HTTP turns Secure off, or the browser never sends the
// cookie back and every guest form is refused.
func TestSecureFalseLeavesTheGuestCookieUsableOverPlainHTTP(t *testing.T) {
	_, _, guest := issuing(t, security.NewCSRF(appKey, time.Hour).Secure(false), http.MethodGet)
	if guest == nil || guest.Secure {
		t.Fatalf("guest cookie = %+v, want one without Secure", guest)
	}
}

// OPTIONS and TRACE draw no page, so nothing is issued and no cookie is set.
func TestARequestThatDrawsNoPageIsIssuedNothing(t *testing.T) {
	for _, method := range []string{http.MethodOptions, http.MethodTrace} {
		_, found, guest := issuing(t, security.NewCSRF(appKey, time.Hour), method)
		if found || guest != nil {
			t.Errorf("%s: token on context = %v, guest cookie = %v; want neither", method, found, guest)
		}
	}
}

// A request that passed the check carries the token it submitted, so a form
// drawn again on that same request posts a token that still validates.
func TestAnAcceptedRequestCarriesItsTokenOnTheContext(t *testing.T) {
	csrf := security.NewCSRF(appKey, time.Hour)
	token, err := csrf.Issue("session-1")
	if err != nil {
		t.Fatal(err)
	}

	var onContext string
	h := fhttp.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		onContext, _ = hhttp.CSRFTokenFrom(r.Context())
	}), middleware.CSRFProtect(csrf, func(*http.Request) string { return "session-1" }))

	r := httptest.NewRequest(http.MethodPost, "/invoices", nil)
	r.Header.Set("X-CSRF-Token", token)
	h.ServeHTTP(httptest.NewRecorder(), r)

	if onContext != token {
		t.Fatalf("token on the context = %q, want the one submitted", onContext)
	}
}

// A request with a session is bound to the session and is not handed a guest
// cookie it has no use for.
func TestASessionIsNotGivenAGuestCookie(t *testing.T) {
	csrf := security.NewCSRF(appKey, time.Hour)
	h := fhttp.Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		middleware.CSRFProtect(csrf, func(*http.Request) string { return "session-1" }))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	for _, c := range rec.Result().Cookies() {
		if c.Name == session.GuestCookieName {
			t.Fatalf("a request with a session was given a %s cookie", session.GuestCookieName)
		}
	}
}

// A token bound to the empty id was one token for every visitor. Validation
// with no session and no guest cookie now refuses even a token signed that way.
func TestATokenWithNoBindingIsRefused(t *testing.T) {
	h, _ := csrfHandler("")
	guestless, err := security.NewCSRF(appKey, time.Hour).Issue("guest:someone-else")
	if err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest(http.MethodPost, "/invoices", nil)
	r.Header.Set("X-CSRF-Token", guestless)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Code != middleware.StatusCSRFExpired {
		t.Fatalf("status = %d, want %d for a request that carries neither a session nor a guest cookie",
			rec.Code, middleware.StatusCSRFExpired)
	}
}
