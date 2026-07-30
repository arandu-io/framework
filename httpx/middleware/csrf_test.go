package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/framework/httpx"
	"github.com/arandu-io/framework/httpx/middleware"
	"github.com/arandu-io/framework/security"
)

var appKey = []byte("0123456789abcdef0123456789abcdef")

func csrfHandler(sessionID string) (http.Handler, *security.CSRF) {
	csrf := security.NewCSRF(appKey, time.Hour)
	reached := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("reached"))
	})
	h := httpx.Chain(reached, middleware.CSRFProtect(csrf, func(*http.Request) string { return sessionID }))
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
