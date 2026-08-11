package httpx_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/arandu-io/framework/httpx"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/framework/validation"
)

func rejectingRouter() *httpx.Router {
	r := httpx.NewRouter().WithFlash(security.NewFlash([]byte(strings.Repeat("k", 32)), false))
	r.Action(http.MethodPost, "/posts", func(ctx *httpx.Context) error {
		errs := validation.Errors{}
		errs.Add("title", "is required")
		return errs
	})
	return r
}

func submitted(target string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, target, strings.NewReader("title=&body=a+draft"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}

func TestAReturnedValidationErrorRedirectsBackInsteadOfPanicking(t *testing.T) {
	req := submitted("http://example.test/posts")
	req.Header.Set("Referer", "http://example.test/posts/new")

	rec := httptest.NewRecorder()
	rejectingRouter().ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("answered %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if to := rec.Header().Get("Location"); to != "/posts/new" {
		t.Errorf("sent to %q, want the form it came from", to)
	}
	if rec.Header().Get("Set-Cookie") == "" {
		t.Error("no flash was left behind, so the form comes back with nothing on it")
	}
	// One person's messages and one person's typed input must not be kept by a
	// cache shared between people.
	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

func TestARejectedFormUnderHTMXGetsHXRedirect(t *testing.T) {
	req := submitted("http://example.test/posts")
	req.Header.Set("Referer", "http://example.test/posts/new")
	req.Header.Set("HX-Request", "true")

	rec := httptest.NewRecorder()
	rejectingRouter().ServeHTTP(rec, req)

	// htmx does not swap a 4xx, so a body would be fetched and thrown away --
	// which is the failure this whole path exists to remove.
	if to := rec.Header().Get("HX-Redirect"); to != "/posts/new" {
		t.Errorf("HX-Redirect = %q, want %q", to, "/posts/new")
	}
	if rec.Code != http.StatusNoContent {
		t.Errorf("answered %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestRejectRefusesAForeignRefererAndFallsBackToRoot(t *testing.T) {
	// The address ends up in a Location header and comes off a header the
	// sender chose. Every one of these is somebody else's site.
	for _, referer := range []string{
		"https://evil.example/login",
		"http://evil.example/posts/new",
		"//evil.example/x",
		"javascript:alert(1)",
		"",
	} {
		t.Run(referer, func(t *testing.T) {
			req := submitted("http://example.test/posts")
			if referer != "" {
				req.Header.Set("Referer", referer)
			}

			rec := httptest.NewRecorder()
			rejectingRouter().ServeHTTP(rec, req)

			if to := rec.Header().Get("Location"); to != "/" {
				t.Errorf("Referer %q sent the browser to %q", referer, to)
			}
		})
	}
}

func TestBackKeepsTheQueryOfThePageTheFormWasOn(t *testing.T) {
	// A form reached at /posts/new?from=drafts must come back to the same list,
	// or the person lands on a page that has lost where they were.
	req := httptest.NewRequest(http.MethodPost, "http://example.test/posts", nil)
	req.Header.Set("Referer", "http://example.test/posts/new?from=drafts")

	if got := httpx.Back(req); got != "/posts/new?from=drafts" {
		t.Errorf("Back = %q, want the address with its query", got)
	}
}

func TestAHandlerErrorThatIsNotARejectionStillPanics(t *testing.T) {
	r := httpx.NewRouter().WithFlash(security.NewFlash([]byte(strings.Repeat("k", 32)), false))
	r.Action(http.MethodGet, "/boom", func(ctx *httpx.Context) error {
		return errors.New("the database is down")
	})

	defer func() {
		if recover() == nil {
			t.Error("a failure the handler could not handle was swallowed, which answers 200 with an empty body")
		}
	}()
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/boom", nil))
}

func TestARejectionWithNoFlashWiredPanicsRatherThanVanishing(t *testing.T) {
	// Without a flash there is nowhere to put the messages, so redirecting would
	// send the person back to the form with nothing on it -- the original bug,
	// produced by the fix. It is a wiring failure and it says so.
	r := httpx.NewRouter()
	r.Action(http.MethodPost, "/posts", func(ctx *httpx.Context) error {
		errs := validation.Errors{}
		errs.Add("title", "is required")
		return errs
	})

	defer func() {
		if recover() == nil {
			t.Error("a rejection with no flash wired was silently redirected")
		}
	}()
	r.ServeHTTP(httptest.NewRecorder(), submitted("/posts"))
}

func TestAnEmptyRejectionIsADefectAndSaysSo(t *testing.T) {
	// `return errs` without asking whether anything failed sends the person back
	// to the form they just filled in, with no reason given.
	r := httpx.NewRouter().WithFlash(security.NewFlash([]byte(strings.Repeat("k", 32)), false))
	r.Action(http.MethodPost, "/posts", func(ctx *httpx.Context) error {
		return validation.Errors{}
	})

	defer func() {
		got, ok := recover().(string)
		if !ok || !strings.Contains(got, "empty validation.Errors") {
			t.Errorf("panicked with %v, want a message naming the mistake", got)
		}
	}()
	r.ServeHTTP(httptest.NewRecorder(), submitted("/posts"))
}

func TestTheFlashSurvivesAGroup(t *testing.T) {
	// Group builds a Router by hand rather than copying it, so a field added to
	// the struct and not to Group is a field that silently stops existing on
	// every grouped route -- which is most of them.
	r := httpx.NewRouter().WithFlash(security.NewFlash([]byte(strings.Repeat("k", 32)), false))
	admin := r.Group("/admin")
	admin.Action(http.MethodPost, "/posts", func(ctx *httpx.Context) error {
		errs := validation.Errors{}
		errs.Add("title", "is required")
		return errs
	})

	req := submitted("http://example.test/admin/posts")
	req.Header.Set("Referer", "http://example.test/admin/posts/new")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("a grouped route answered %d: the flash did not survive Group", rec.Code)
	}
}

func TestOldInputIsOnTheRequestAfterTheFlashIsConsumed(t *testing.T) {
	// The handler-side reading of what was typed, for the controller that needs
	// the value in Go rather than in markup.
	state := httpx.State{Old: map[string][]string{"email": {"ada@example.test"}}}

	r := httpx.NewRouter()
	r.Action(http.MethodGet, "/signup", func(ctx *httpx.Context) error {
		if got := ctx.Old("email"); got != "ada@example.test" {
			t.Errorf("ctx.Old = %q", got)
		}
		if got := ctx.Old("password"); got != "" {
			t.Errorf("ctx.Old(password) = %q, want empty always", got)
		}
		return nil
	})

	req := httptest.NewRequest(http.MethodGet, "/signup", nil)
	r.ServeHTTP(httptest.NewRecorder(), req.WithContext(httpx.WithState(req.Context(), state)))
}

func TestAPageWithNoRejectionBehindItHasNoState(t *testing.T) {
	// The zero State is the answer for nearly every request, and it must not
	// need a nil check at any call site.
	r := httpx.NewRouter()
	r.Action(http.MethodGet, "/", func(ctx *httpx.Context) error {
		if ctx.State().Errors.Any() {
			t.Error("errors appeared on a request nobody was rejected on")
		}
		if got := ctx.Old("anything"); got != "" {
			t.Errorf("Old on a fresh request = %q", got)
		}
		return nil
	})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}
