package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRedirectAnswersWhatTheClientCanActOn pins the branch, because the version
// without it left a browser on a blank page.
//
// Signing in used to answer 200 with HX-Redirect and no body, whatever asked.
// HTMX reads that header and navigates; nothing else does. A form posted with
// scripts off got a successful, empty, silent page -- the session was created
// and the person was left staring at white.
//
// It is the same rule http.Context.Redirect applies, and the reason this test
// exists is that having the rule in two places is what let one of them be wrong.
func TestRedirectAnswersWhatTheClientCanActOn(t *testing.T) {
	t.Run("HTMX gets the header it reads", func(t *testing.T) {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
		r.Header.Set("HX-Request", "true")

		redirect(rec, r, "/dashboard")

		if got := rec.Header().Get("HX-Redirect"); got != "/dashboard" {
			t.Errorf("HX-Redirect = %q, want /dashboard", got)
		}
		if rec.Code != http.StatusNoContent {
			t.Errorf("status = %d, want 204", rec.Code)
		}
	})

	t.Run("a plain form post gets a redirect a browser follows", func(t *testing.T) {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/auth/login", nil)

		redirect(rec, r, "/dashboard")

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want 303 -- a browser with no JavaScript has to be sent somewhere", rec.Code)
		}
		if got := rec.Header().Get("Location"); got != "/dashboard" {
			t.Errorf("Location = %q, want /dashboard", got)
		}
	})
}
