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
	hhttp "github.com/arandu-io/hesape/http"
)

// exceptHandler runs CSRFProtect with the given options in front of a handler
// that answers 200, for a visitor with no session.
func exceptHandler(opts ...middleware.CSRFOption) http.Handler {
	csrf := security.NewCSRF(appKey, time.Hour)
	reached := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("reached"))
	})
	return fhttp.Chain(reached, middleware.CSRFProtect(csrf, func(*http.Request) string { return "" }, opts...))
}

// A provider posts a webhook with no session and no token; the path the
// application exempted lets it through to the route that verifies its
// signature.
func TestAnExemptPathTakesAWriteWithoutAToken(t *testing.T) {
	h := exceptHandler(middleware.CSRFExcept("/webhooks/"))

	r := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", strings.NewReader(`{"id":"evt_1"}`))
	r.Header.Set("Content-Type", "application/json")

	if got := serve(h, r); got != http.StatusOK {
		t.Fatalf("status = %d, want 200 on a path CSRFExcept named", got)
	}
}

// An exempt path skips the origin check too: a provider's request is not a
// page of this application, and nothing on it says where it came from.
func TestAnExemptPathSkipsTheOriginCheck(t *testing.T) {
	h := exceptHandler(middleware.CSRFExcept("/webhooks/"))

	r := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", nil)
	r.Header.Set("Origin", "https://hooks.provider.example")

	if got := serve(h, r); got != http.StatusOK {
		t.Fatalf("status = %d, want 200: an exempt path skips the origin check", got)
	}
}

// Matching is by whole segments. Neither form of the prefix reaches a path
// that merely begins with the same letters.
func TestAnExemptPrefixDoesNotCrossASegmentBoundary(t *testing.T) {
	for _, prefix := range []string{"/webhooks/", "/webhooks"} {
		h := exceptHandler(middleware.CSRFExcept(prefix))
		for _, target := range []string{"/webhooksx", "/webhooksx/stripe"} {
			if got := serve(h, httptest.NewRequest(http.MethodPost, target, nil)); got != middleware.StatusCSRFExpired {
				t.Errorf("CSRFExcept(%q), POST %s: status = %d, want %d", prefix, target, got, middleware.StatusCSRFExpired)
			}
		}
	}
}

// A prefix without a trailing slash is one exact path, not a subtree.
func TestAnExemptPathWithoutATrailingSlashIsExact(t *testing.T) {
	h := exceptHandler(middleware.CSRFExcept("/webhooks"))

	if got := serve(h, httptest.NewRequest(http.MethodPost, "/webhooks", nil)); got != http.StatusOK {
		t.Errorf("POST /webhooks: status = %d, want 200", got)
	}
	if got := serve(h, httptest.NewRequest(http.MethodPost, "/webhooks/stripe", nil)); got != middleware.StatusCSRFExpired {
		t.Errorf("POST /webhooks/stripe: status = %d, want %d: \"/webhooks\" names one path", got, middleware.StatusCSRFExpired)
	}
}

// The path is cleaned before it is compared, so dot segments that climb out of
// the exempt prefix land on a path that is checked.
func TestATraversalOutOfAnExemptPrefixIsChecked(t *testing.T) {
	h := exceptHandler(middleware.CSRFExcept("/webhooks/"))

	for _, target := range []string{"/webhooks/../notes", "/webhooks/./../notes/1", "/webhooks/stripe/../../notes"} {
		if got := serve(h, httptest.NewRequest(http.MethodPost, target, nil)); got != middleware.StatusCSRFExpired {
			t.Errorf("POST %s: status = %d, want %d: the cleaned path is not under /webhooks/", target, got, middleware.StatusCSRFExpired)
		}
	}
}

// A path that leaves RawPath set is never exempt, whatever it decodes to.
func TestAnEscapedPathIsNeverExempt(t *testing.T) {
	h := exceptHandler(middleware.CSRFExcept("/webhooks/"))

	for _, target := range []string{"/notes/%2E%2E/webhooks/x", "/webhooks%2Fstripe", "/webhooks/%73tripe%2F"} {
		r := httptest.NewRequest(http.MethodPost, target, nil)
		if r.URL.RawPath == "" {
			t.Fatalf("%s: RawPath is empty, so this case does not test what it names", target)
		}
		if got := serve(h, r); got != middleware.StatusCSRFExpired {
			t.Errorf("POST %s (Path %q): status = %d, want %d", target, r.URL.Path, got, middleware.StatusCSRFExpired)
		}
	}
}

// The end-to-end reason for the RawPath rule, behind the router the framework
// routes with. /notes/%2E%2E/webhooks/x decodes to a path under /webhooks/,
// and http.ServeMux routes it to the notes route, because it matches the
// escaped path segment by segment. If the decoded path were trusted, a write
// on a session route would skip the check.
func TestAnEscapedPathCannotReachASessionRouteThroughTheExemption(t *testing.T) {
	var reached string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /notes/{rest...}", func(w http.ResponseWriter, r *http.Request) { reached = "notes" })
	mux.HandleFunc("POST /webhooks/{provider}", func(w http.ResponseWriter, r *http.Request) { reached = "webhook" })

	const smuggled = "/notes/%2E%2E/webhooks/x"

	// The premise: without the middleware, the router sends it to notes.
	reached = ""
	serve(mux, httptest.NewRequest(http.MethodPost, smuggled, nil))
	if reached != "notes" {
		t.Fatalf("the router sent %s to %q, want notes: the premise this test guards no longer holds", smuggled, reached)
	}

	csrf := security.NewCSRF(appKey, time.Hour)
	h := fhttp.Chain(mux, middleware.CSRFProtect(csrf, func(*http.Request) string { return "" }, middleware.CSRFExcept("/webhooks/")))

	reached = ""
	if got := serve(h, httptest.NewRequest(http.MethodPost, smuggled, nil)); got != middleware.StatusCSRFExpired || reached != "" {
		t.Fatalf("POST %s: status = %d, reached %q; want %d and no handler", smuggled, got, reached, middleware.StatusCSRFExpired)
	}

	reached = ""
	if got := serve(h, httptest.NewRequest(http.MethodPost, "/notes/1", nil)); got != middleware.StatusCSRFExpired || reached != "" {
		t.Fatalf("POST /notes/1: status = %d, reached %q; want %d and no handler", got, reached, middleware.StatusCSRFExpired)
	}

	reached = ""
	if got := serve(h, httptest.NewRequest(http.MethodPost, "/webhooks/stripe", nil)); got != http.StatusOK || reached != "webhook" {
		t.Fatalf("POST /webhooks/stripe: status = %d, reached %q; want 200 and the webhook", got, reached)
	}
}

// A page on an exempt path is still issued a token, like any other page.
func TestAGetOnAnExemptPathIsStillIssuedAToken(t *testing.T) {
	csrf := security.NewCSRF(appKey, time.Hour)
	var found bool
	h := fhttp.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, found = hhttp.CSRFTokenFrom(r.Context())
	}), middleware.CSRFProtect(csrf, func(*http.Request) string { return "" }, middleware.CSRFExcept("/webhooks/")))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/webhooks/setup", nil))
	if !found {
		t.Fatal("a GET on an exempt path carried no token, so a form drawn there is refused elsewhere")
	}
}

// Without CSRFExcept nothing is exempt, and several calls add up.
func TestOnlyTheNamedPathsAreExempt(t *testing.T) {
	if got := serve(exceptHandler(), httptest.NewRequest(http.MethodPost, "/webhooks/stripe", nil)); got != middleware.StatusCSRFExpired {
		t.Errorf("no option, POST /webhooks/stripe: status = %d, want %d", got, middleware.StatusCSRFExpired)
	}

	h := exceptHandler(middleware.CSRFExcept("/webhooks/"), middleware.CSRFExcept("/mcp", "/inbound/"))
	for _, target := range []string{"/webhooks/stripe", "/mcp", "/inbound/mail"} {
		if got := serve(h, httptest.NewRequest(http.MethodPost, target, nil)); got != http.StatusOK {
			t.Errorf("POST %s: status = %d, want 200", target, got)
		}
	}
}

// Each wiring mistake panics when the option is built, naming what is wrong.
func TestAWrongExemptionPanicsAtWiring(t *testing.T) {
	cases := []struct {
		name  string
		build func()
		says  string
	}{
		{"no leading slash", func() { middleware.CSRFExcept("webhooks/") }, "does not begin with '/'"},
		{"empty", func() { middleware.CSRFExcept("") }, "does not begin with '/'"},
		{"the root", func() { middleware.CSRFExcept("/") }, "exempts every route"},
		{"dot segments", func() { middleware.CSRFExcept("/webhooks/../notes/") }, "not a clean path"},
		{"repeated slash", func() { middleware.CSRFExcept("//webhooks/") }, "not a clean path"},
		{"a nil option", func() {
			middleware.CSRFProtect(security.NewCSRF(appKey, time.Hour), func(*http.Request) string { return "" }, nil)
		}, "nil CSRFOption"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				got := recover()
				msg, _ := got.(string)
				if !strings.Contains(msg, tc.says) {
					t.Fatalf("panic = %v, want one saying %q", got, tc.says)
				}
			}()
			tc.build()
		})
	}
}
