package feature

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/http/middleware"
	"github.com/arandu-io/framework/security"
)

var appKey = []byte("0123456789abcdef0123456789abcdef")

func csrfHandler(sessionID string) (http.Handler, *security.CSRF) {
	csrf := security.NewCSRF(appKey, time.Hour)
	reached := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("reached"))
	})
	h := fhttp.Chain(reached, middleware.CSRFProtect(csrf, func(*http.Request) string { return sessionID }))
	return h, csrf
}

func TestCSRFAcceptsTheHeader(t *testing.T) {
	h, csrf := csrfHandler("session-1")
	token, err := csrf.Issue("session-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	r := httptest.NewRequest(http.MethodPost, "/invoices", nil)
	r.Header.Set("X-CSRF-Token", token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with a valid header token", rec.Code)
	}
}

// TestCSRFAcceptsTheFormField covers the plain HTML form, which must keep working
// for anyone who is not using HTMX.
func TestCSRFAcceptsTheFormField(t *testing.T) {
	h, csrf := csrfHandler("session-1")
	token, err := csrf.Issue("session-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	r := httptest.NewRequest(http.MethodPost, "/invoices", strings.NewReader("_csrf="+token))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with a valid form token", rec.Code)
	}
}

func TestCSRFRejectsMissingToken(t *testing.T) {
	h, _ := csrfHandler("session-1")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/invoices", nil))

	if rec.Code != middleware.StatusCSRFExpired {
		t.Fatalf("status = %d, want %d", rec.Code, middleware.StatusCSRFExpired)
	}
}

// TestCSRFRejectsATokenFromAnotherSession is the property that makes
// double-submit worth anything.
func TestCSRFRejectsATokenFromAnotherSession(t *testing.T) {
	h, csrf := csrfHandler("session-1")
	stolen, err := csrf.Issue("session-2")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	r := httptest.NewRequest(http.MethodPost, "/invoices", nil)
	r.Header.Set("X-CSRF-Token", stolen)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Code != middleware.StatusCSRFExpired {
		t.Fatalf("status = %d, want the token to be refused", rec.Code)
	}
}

// The token is not the only thing that decides any more. A page on another site
// that got hold of a valid token used to be enough on its own, and the browser
// says where the request came from long before the HMAC is computed.
func TestCSRFRefusesACrossSiteRequestCarryingAValidToken(t *testing.T) {
	h, csrf := csrfHandler("session-1")
	token, err := csrf.Issue("session-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	for _, tc := range []struct{ name, header, value string }{
		{"the browser reports another site", "Sec-Fetch-Site", "cross-site"},
		{"the browser reports a sibling host", "Sec-Fetch-Site", "same-site"},
		{"an older browser sends only Origin", "Origin", "https://attacker.example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/invoices", nil)
			r.Header.Set("X-CSRF-Token", token)
			r.Header.Set(tc.header, tc.value)

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403: the token was valid and the request was not ours", rec.Code)
			}
			if rec.Code == middleware.StatusCSRFExpired {
				t.Error("a cross-origin request was answered as an expired token, so the page would reload into the same refusal")
			}
			if !strings.Contains(rec.Body.String(), "cross-origin") {
				t.Errorf("the refusal does not say what was wrong: %q", rec.Body.String())
			}
		})
	}
}

// The other half: the application's own pages must keep working, whichever of
// the two headers the browser sends.
func TestCSRFLetsTheApplicationsOwnPagesThrough(t *testing.T) {
	h, csrf := csrfHandler("session-1")
	token, err := csrf.Issue("session-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	for _, tc := range []struct{ name, header, value string }{
		{"the browser reports the same origin", "Sec-Fetch-Site", "same-origin"},
		{"the browser reports no origin at all", "Sec-Fetch-Site", "none"},
		{"an older browser sends a matching Origin", "Origin", "https://example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/invoices", nil)
			r.Header.Set("X-CSRF-Token", token)
			r.Header.Set(tc.header, tc.value)

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: this is the site posting to itself", rec.Code)
			}
		})
	}
}

func TestCSRFLetsSafeMethodsThrough(t *testing.T) {
	h, _ := csrfHandler("session-1")

	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, "/invoices", nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s = %d, want 200: a read must not need a token", method, rec.Code)
		}
	}
}

// TestCSRFGuardsEveryWriteMethod: protecting POST only is a common and expensive
// mistake, because HTMX issues PUT, PATCH and DELETE directly.
func TestCSRFGuardsEveryWriteMethod(t *testing.T) {
	h, _ := csrfHandler("session-1")

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, "/invoices", nil))
		if rec.Code == http.StatusOK {
			t.Errorf("%s passed without a token", method)
		}
	}
}

// An expired token reaches a person as nothing at all under HTMX: htmx does not
// swap a 419, so the sentence telling them to reload was in a body that was
// thrown away, and the button they pressed simply did nothing.
//
// HX-Refresh reloads the page, which is the only useful reaction to a token that
// is no longer valid -- the reload brings a fresh one. The role guard answers the
// same way for the same reason; changing one of the two would have left the
// framework refusing an HTMX request in two different shapes.
func TestAnExpiredTokenTellsHtmxToReloadRatherThanLeavingTheScreenUnchanged(t *testing.T) {
	h, _ := csrfHandler("session-1")

	for name, r := range map[string]*http.Request{
		"no token at all": httptest.NewRequest(http.MethodPost, "/invoices", nil),
		"a token from another session": httptest.NewRequest(http.MethodPost, "/invoices",
			strings.NewReader("_csrf=not-this-sessions-token")),
	} {
		t.Run(name, func(t *testing.T) {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.Header.Set("HX-Request", "true")

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)

			if rec.Header().Get("HX-Refresh") != "true" {
				t.Error("htmx was given nothing to do, so the form did nothing and said nothing")
			}
			if rec.Code != middleware.StatusCSRFExpired {
				t.Errorf("status = %d, want %d unchanged", rec.Code, middleware.StatusCSRFExpired)
			}
		})
	}
}

// A form posted without JavaScript already renders the body, and must not be
// told to reload: the reload would replace the explanation with the page they
// were on.
func TestAPlainFormPostIsNotToldToReload(t *testing.T) {
	h, _ := csrfHandler("session-1")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/invoices", nil))

	if got := rec.Header().Get("HX-Refresh"); got != "" {
		t.Errorf("HX-Refresh = %q on a request no htmx sent", got)
	}
	if !strings.Contains(rec.Body.String(), "_csrf") {
		t.Errorf("the refusal does not say what is missing: %q", rec.Body.String())
	}
}
