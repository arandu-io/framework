package unit

import (
	"net/http"
	"net/http/httptest"
	"testing"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/view"
	hhttp "github.com/arandu-io/hesape/http"
)

// The controller never passes the token: the middleware that protects forms
// puts it on the request context, and New reads it from there.
func TestNewTakesTheTokenFromTheRequestContext(t *testing.T) {
	var page view.Page
	r := fhttp.NewRouter()
	r.Action(http.MethodGet, "/signin", func(ctx *fhttp.Context) error {
		page = view.New(ctx, "Sign in")
		return nil
	})

	req := httptest.NewRequest(http.MethodGet, "/signin", nil)
	r.ServeHTTP(httptest.NewRecorder(), req.WithContext(hhttp.WithCSRFToken(req.Context(), "issued-token")))

	if page.CSRFToken() != "issued-token" {
		t.Fatalf("CSRFToken = %q, want the token on the request context", page.CSRFToken())
	}
}

// WithToken still replaces it, for a page drawn where no middleware issued one.
func TestWithTokenReplacesTheTokenFromTheContext(t *testing.T) {
	var page view.Page
	r := fhttp.NewRouter()
	r.Action(http.MethodGet, "/signin", func(ctx *fhttp.Context) error {
		page = view.New(ctx, "Sign in").WithToken("explicit")
		return nil
	})

	req := httptest.NewRequest(http.MethodGet, "/signin", nil)
	r.ServeHTTP(httptest.NewRecorder(), req.WithContext(hhttp.WithCSRFToken(req.Context(), "issued-token")))

	if page.CSRFToken() != "explicit" {
		t.Fatalf("CSRFToken = %q, want the one WithToken set", page.CSRFToken())
	}
}
