package unit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/arandu-io/framework/http/middleware"
	"github.com/arandu-io/framework/security"
	hmiddleware "github.com/arandu-io/hesape/http/middleware"
)

// The guards' types are hesape/http/middleware's under the old names: one
// type each, so a resolver or a store written for either package is accepted
// by both.
var (
	_ hmiddleware.CSRFOption       = middleware.CSRFExcept("/webhooks")
	_ middleware.CSRFOption        = hmiddleware.CSRFExcept("/webhooks")
	_ hmiddleware.TokenDigest      = middleware.DigestToken("t")
	_ hmiddleware.TokenResolver    = middleware.TokenResolver(nil)
	_ hmiddleware.IdempotencyStore = middleware.IdempotencyStore(nil)
	_ hmiddleware.Sessions         = middleware.Sessions(nil)
	_ middleware.Sessions          = (*security.SessionStore)(nil)
)

// TestTheForwardsAreHesapesGuards: each forward answers what the hesape
// function it names answers, so a request is refused the same way whichever
// name an application wired.
func TestTheForwardsAreHesapesGuards(t *testing.T) {
	if middleware.StatusCSRFExpired != hmiddleware.StatusCSRFExpired ||
		middleware.SignInPath != hmiddleware.SignInPath ||
		middleware.PasswordConfirmPath != hmiddleware.PasswordConfirmPath ||
		middleware.ErrUnknownToken != hmiddleware.ErrUnknownToken {
		t.Fatal("a constant or an error differs from hesape's")
	}
	if middleware.DigestToken("secret") != hmiddleware.DigestToken("secret") {
		t.Fatal("DigestToken differs from hesape's")
	}

	sessions := security.NewSessionStore([]byte("0123456789abcdef0123456789abcdef"), time.Hour, false, nil)
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	for name, guard := range map[string]func(http.Handler) http.Handler{
		"RequireAuth":              middleware.RequireAuth(sessions),
		"RequireRole":              middleware.RequireRole(sessions, "admin"),
		"RequireConfirmedPassword": middleware.RequireConfirmedPassword(sessions),
	} {
		rec := httptest.NewRecorder()
		guard(ok).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/panel", nil))
		if rec.Code != http.StatusSeeOther && rec.Code != http.StatusFound {
			t.Errorf("%s let a request with no session through: %d", name, rec.Code)
		}
		if got := rec.Header().Get("Location"); got != middleware.SignInPath {
			t.Errorf("%s sent a visitor to %q, want %q", name, got, middleware.SignInPath)
		}
	}

	rec := httptest.NewRecorder()
	middleware.RequireToken(noTokens{})(ok).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("RequireToken let a request with no token through: %d", rec.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.7:1234"
	if got, want := middleware.KeyBySession(sessions)(req), hmiddleware.KeyBySession(sessions)(req); got != want {
		t.Errorf("KeyBySession keyed %q, hesape keys %q", got, want)
	}
}

// noTokens knows no token at all.
type noTokens struct{}

func (noTokens) ResolveToken(context.Context, middleware.TokenDigest) (security.Subject, error) {
	return security.Subject{}, middleware.ErrUnknownToken
}
