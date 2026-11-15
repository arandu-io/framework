//go:build e2e

package e2e

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/http/middleware"
	"github.com/arandu-io/framework/modules/auth"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/framework/test/browser"
	"github.com/arandu-io/framework/test/browser/cdp"
	"github.com/arandu-io/hesape/view"
)

// The sign-in screen the framework ships, in a real browser under the CSP the
// framework sets: YPY loads from the embedded copy, the csrf extension
// registers, and a request YPY sends carries the token of the meta tag.
func TestTheSignInScreenRunsYPYAndSendsItsToken(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	svc := auth.NewService(auth.NewUserRepo(nil), security.NewSessionStore(key, time.Hour, false, security.NewMemoryBackend()), security.NewCSRF(key, time.Hour))
	router := fhttp.NewRouter()
	auth.New(svc, auth.FixedTenant("tenant-1")).Routes(router)

	mux := http.NewServeMux()
	mux.HandleFunc(view.AssetPath, view.Handler)
	mux.HandleFunc("POST /probe", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<p id="probe">token=%s</p>`, r.Header.Get("X-CSRF-Token"))
	})
	mux.Handle("/", router)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "script-src 'self'")
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)

	page := browser.Open(t, browser.Registry{"cdp": cdp.Launch}, browser.BaseURL(server.URL))
	page.Visit(middleware.SignInPath)

	var state struct {
		YPY   bool   `json:"ypy"`
		Token string `json:"token"`
		Boost bool   `json:"boost"`
	}
	page.Evaluate(`({ypy: typeof ypy === 'object', token: document.querySelector('meta[name="ypy-csrf"]').content, boost: document.body.getAttribute('ypy-boost:inherited') === 'true'})`, &state)
	if !state.YPY || state.Token == "" || !state.Boost {
		t.Fatalf("the sign-in screen did not start YPY with a token: %+v", state)
	}

	// A request YPY sends from this page carries the token, with no header
	// written on any element.
	page.Evaluate(`(async () => { document.body.insertAdjacentHTML('beforeend', '<div id="out"></div>'); await ypy.ajax('POST', '/probe', '#out'); return true })()`, nil)
	var got string
	page.Evaluate(`document.getElementById('out').textContent`, &got)
	if got != "token="+state.Token {
		t.Fatalf("the request YPY sent carried %q, want the token of the meta tag %q", got, state.Token)
	}
}
