package foundation_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/arandu-io/framework/config"
	"github.com/arandu-io/framework/foundation"
	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/framework/validation"
)

// flashModule is an application that rejects a form and, on the way back,
// reports what the framework handed it.
type flashModule struct{ seen chan fhttp.State }

func (flashModule) Name() string { return "flash" }

func (m flashModule) Routes(r *fhttp.Router) {
	r.Action(http.MethodPost, "/posts", func(ctx *fhttp.Context) error {
		errs := validation.Errors{}
		errs.Add("title", "is required")
		return errs
	})
	r.Action(http.MethodGet, "/posts/new", func(ctx *fhttp.Context) error {
		m.seen <- ctx.State()
		return ctx.Status(http.StatusOK)
	})
}

// TestTheFlashMiddlewareIsInstalledWithoutTheApplicationWiringIt is the property
// that makes this feature exist rather than merely be available.
//
// bootstrap/app.go below is the whole application: two routes and no pipeline.
// It calls no Use(), registers no middleware and passes no flash. A wiring line
// an application can leave out is one an application leaves out, and the symptom
// -- a form that comes back blank -- is indistinguishable from the bug this was
// built to fix.
func TestTheFlashMiddlewareIsInstalledWithoutTheApplicationWiringIt(t *testing.T) {
	seen := make(chan fhttp.State, 1)
	k := foundation.New(testConfig(config.EnvProd)).Register(flashModule{seen: seen})
	if err := k.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	handler := k.Handler()

	form := strings.NewReader("title=&body=a+draft")
	post := httptest.NewRequest(http.MethodPost, "http://example.test/posts", form)
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.Header.Set("Referer", "http://example.test/posts/new")
	rejected := httptest.NewRecorder()
	handler.ServeHTTP(rejected, post)

	if rejected.Code != http.StatusSeeOther {
		t.Fatalf("the Application's router did not answer the rejection: %d", rejected.Code)
	}

	var flash *http.Cookie
	for _, c := range (&http.Response{Header: rejected.Header()}).Cookies() {
		if c.Name == security.FlashCookieName {
			flash = c
		}
	}
	if flash == nil {
		t.Fatal("no flash cookie: the Application did not wire one into the router")
	}
	// Secure outside development, because it carries what somebody typed.
	if !flash.Secure {
		t.Error("the flash cookie is not Secure in production")
	}

	get := httptest.NewRequest(http.MethodGet, "http://example.test/posts/new", nil)
	get.Header.Set("Accept", "text/html")
	get.AddCookie(flash)
	handler.ServeHTTP(httptest.NewRecorder(), get)

	state := <-seen
	if got := state.Errors["title"]; len(got) != 1 || got[0] != "is required" {
		t.Errorf("the handler was given %v: the Application did not install middleware.Flash", state.Errors)
	}
	if got := state.Old.Get("body"); got != "a draft" {
		t.Errorf("old input = %q", got)
	}
}

// TestTheConsoleDoesNotSpendTheFlash keeps a debugging tool from eating the
// thing being debugged.
//
// The console is a page a browser asks for with text/html, so without the
// framework's own routes being excluded it would consume the one-shot flash --
// and the form the person was sent back to would come back blank, but only with
// the console open, which is precisely when somebody is investigating something
// else.
func TestTheConsoleDoesNotSpendTheFlash(t *testing.T) {
	f := security.NewFlash(testConfig(config.EnvDev).AppKey, false)
	rec := httptest.NewRecorder()
	f.Write(rec, map[string][]string{"title": {"is required"}}, url.Values{})
	var flash *http.Cookie
	for _, c := range (&http.Response{Header: rec.Header()}).Cookies() {
		if c.Name == security.FlashCookieName {
			flash = c
		}
	}

	seen := make(chan fhttp.State, 1)
	k := foundation.New(testConfig(config.EnvDev)).Register(flashModule{seen: seen})
	if err := k.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	handler := k.Handler()

	console := httptest.NewRequest(http.MethodGet, "http://example.test/_arandu/debug", nil)
	console.Header.Set("Accept", "text/html")
	console.AddCookie(flash)
	spent := httptest.NewRecorder()
	handler.ServeHTTP(spent, console)

	for _, c := range (&http.Response{Header: spent.Header()}).Cookies() {
		if c.Name == security.FlashCookieName && c.MaxAge < 0 {
			t.Fatal("the console cleared the flash")
		}
	}

	page := httptest.NewRequest(http.MethodGet, "http://example.test/posts/new", nil)
	page.Header.Set("Accept", "text/html")
	page.AddCookie(flash)
	handler.ServeHTTP(httptest.NewRecorder(), page)

	if state := <-seen; !state.Errors.Any() {
		t.Error("the page rendered with nothing on it after the console was opened")
	}
}
