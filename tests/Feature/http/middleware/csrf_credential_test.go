package feature

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/http/middleware"
	"github.com/arandu-io/framework/security"
)

// credentialHandler runs CSRFProtect with the real session store's reader, so a
// cookie whose signature does not verify is judged by the code an application
// wires and not by a stand-in that agrees with the test.
func credentialHandler(sessions *security.SessionStore) (http.Handler, *security.CSRF) {
	csrf := security.NewCSRF(appKey, time.Hour)
	reached := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("reached"))
	})
	return fhttp.Chain(reached, middleware.CSRFProtect(csrf, sessions.IDFromRequest)), csrf
}

// junkSessionCookie is a session cookie somebody wrote by hand: the right name,
// a value no key signed.
func junkSessionCookie() *http.Cookie {
	return &http.Cookie{Name: security.SessionCookieName, Value: "forged.not-a-signature"}
}

func serve(h http.Handler, r *http.Request) int {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec.Code
}

// An API client that sends a bearer token and no cookie is left to the guard on
// its route; asking it for a CSRF token is asking for something only a page of
// this application could have handed it.
func TestABearerRequestWithNoSessionIsLeftToItsGuard(t *testing.T) {
	h, _ := credentialHandler(newSessions())

	r := httptest.NewRequest(http.MethodPost, "/api/notes", nil)
	r.Header.Set("Authorization", "Bearer api-token-1")

	if got := serve(h, r); got != http.StatusOK {
		t.Fatalf("status = %d, want 200: a bearer request with no session carries no ambient credential", got)
	}
}

// The scheme is compared the way RequireToken compares it, so a client that
// writes it in lower case is not refused here and accepted there.
func TestTheBearerSchemeIsReadCaseInsensitively(t *testing.T) {
	h, _ := credentialHandler(newSessions())

	r := httptest.NewRequest(http.MethodPost, "/api/notes", nil)
	r.Header.Set("Authorization", "bearer api-token-1")

	if got := serve(h, r); got != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a lower-case bearer scheme", got)
	}
}

// A cookie whose signature does not verify is no session, so a bearer request
// that happens to carry one is still left to its guard.
func TestABearerRequestWithAForgedSessionCookieIsLeftToItsGuard(t *testing.T) {
	h, _ := credentialHandler(newSessions())

	r := httptest.NewRequest(http.MethodPost, "/api/notes", nil)
	r.Header.Set("Authorization", "Bearer api-token-1")
	r.AddCookie(junkSessionCookie())

	if got := serve(h, r); got != http.StatusOK {
		t.Fatalf("status = %d, want 200: the cookie carries no valid session", got)
	}
}

// The forged cross-site request: a junk cookie and no Authorization header. An
// invalid cookie yields no session id, and that alone must not exempt anything.
func TestAForgedSessionCookieWithoutABearerIsRefused(t *testing.T) {
	h, _ := credentialHandler(newSessions())

	r := httptest.NewRequest(http.MethodPost, "/notes", nil)
	r.AddCookie(junkSessionCookie())

	if got := serve(h, r); got != middleware.StatusCSRFExpired {
		t.Fatalf("status = %d, want %d: no bearer token, so the CSRF token is required", got, middleware.StatusCSRFExpired)
	}
}

// With a valid session cookie the route may honour the cookie and ignore the
// header, so a bearer header does not lift the check.
func TestABearerHeaderDoesNotExemptASignedInRequest(t *testing.T) {
	sessions := newSessions()
	h, _ := credentialHandler(sessions)

	r := signedIn(t, sessions, security.Subject{ID: "u-1", Tenant: "acme"}, http.MethodPost, "/notes")
	r.Header.Set("Authorization", "Bearer api-token-1")

	if got := serve(h, r); got != middleware.StatusCSRFExpired {
		t.Fatalf("status = %d, want %d: the session cookie is ambient, bearer or not", got, middleware.StatusCSRFExpired)
	}
}

// A signed-in request with a valid token still passes when it also carries a
// bearer header: the full check is applied, not a refusal.
func TestASignedInRequestWithABearerAndAValidTokenPasses(t *testing.T) {
	sessions := newSessions()
	h, csrf := credentialHandler(sessions)

	r := signedIn(t, sessions, security.Subject{ID: "u-1", Tenant: "acme"}, http.MethodPost, "/notes")
	r.Header.Set("Authorization", "Bearer api-token-1")
	token, err := csrf.Issue(sessions.IDFromRequest(r))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	r.Header.Set("X-CSRF-Token", token)

	if got := serve(h, r); got != http.StatusOK {
		t.Fatalf("status = %d, want 200 with a valid token for the session", got)
	}
}

// The cookie-only write with no token, through the real session reader.
func TestASignedInRequestWithoutATokenIsRefused(t *testing.T) {
	sessions := newSessions()
	h, _ := credentialHandler(sessions)

	r := signedIn(t, sessions, security.Subject{ID: "u-1", Tenant: "acme"}, http.MethodPost, "/notes")

	if got := serve(h, r); got != middleware.StatusCSRFExpired {
		t.Fatalf("status = %d, want %d", got, middleware.StatusCSRFExpired)
	}
}

// Basic, Digest and Negotiate are attached again by the browser after a 401
// challenge, so they are ambient and checked like a cookie. An empty bearer
// token is no credential at all.
func TestAnAmbientOrEmptyAuthorizationIsChecked(t *testing.T) {
	for _, value := range []string{
		"Basic dXNlcjpwYXNz",
		`Digest username="user", realm="app", nonce="n", uri="/notes", response="r"`,
		"Negotiate YIIBhgYGKwYBBQUCoIIBejCCAXag",
		"Bearer ",
		"Bearer",
		"Bearerx token",
	} {
		h, _ := credentialHandler(newSessions())
		r := httptest.NewRequest(http.MethodPost, "/notes", nil)
		r.Header.Set("Authorization", value)

		if got := serve(h, r); got != middleware.StatusCSRFExpired {
			t.Errorf("Authorization %q: status = %d, want %d", value, got, middleware.StatusCSRFExpired)
		}
	}
}

// The origin check still runs before the bearer rule: a browser that reports
// the request as cross-origin is refused whatever header it carries.
func TestABearerRequestTheBrowserReportsAsCrossOriginIsRefused(t *testing.T) {
	h, _ := credentialHandler(newSessions())

	r := httptest.NewRequest(http.MethodPost, "/api/notes", nil)
	r.Header.Set("Authorization", "Bearer api-token-1")
	r.Header.Set("Sec-Fetch-Site", "cross-site")

	if got := serve(h, r); got != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: the origin check applies to a bearer request too", got)
	}
}

// A signed-in request from another site is refused even with a valid token.
func TestASignedInCrossOriginRequestIsRefused(t *testing.T) {
	sessions := newSessions()
	h, csrf := credentialHandler(sessions)

	r := signedIn(t, sessions, security.Subject{ID: "u-1", Tenant: "acme"}, http.MethodPost, "/notes")
	token, err := csrf.Issue(sessions.IDFromRequest(r))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	r.Header.Set("X-CSRF-Token", token)
	r.Header.Set("Sec-Fetch-Site", "cross-site")

	if got := serve(h, r); got != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", got)
	}
}

// A bearer request with no session binds no guest cookie: it is not a browser
// visit, and a cookie set on it would be the first ambient credential it had.
func TestABearerRequestIsNotGivenAGuestCookie(t *testing.T) {
	h, _ := credentialHandler(newSessions())

	r := httptest.NewRequest(http.MethodPost, "/api/notes", nil)
	r.Header.Set("Authorization", "Bearer api-token-1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if cookies := rec.Result().Cookies(); len(cookies) != 0 {
		t.Fatalf("cookies set = %v, want none", cookies)
	}
}
