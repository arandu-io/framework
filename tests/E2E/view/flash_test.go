//go:build e2e

package e2e

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/arandu-io/framework/foundation/bootstrap"
	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/kernel"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/framework/validation"
	"github.com/arandu-io/framework/view"
	"github.com/arandu-io/hesape/config"
	"github.com/arandu-io/hesape/encryption"
)

// The failure this whole path exists to remove, reproduced end to end.
//
// A form was submitted, it was rejected, the messages existed -- and the person
// saw the form again with nothing on it. The messages were in a 422 body that
// htmx threw away, and even where they were not, nothing carried them across the
// redirect that followed. Every hop below is a real one: a real router, a real
// kernel pipeline, a real cookie going into a browser and coming back out, and a
// real view rendering from view.Page.
//
// It is written against the SCREEN rather than against any of the pieces,
// because each piece passed its own test while the person in front of the
// browser saw nothing.

// signupData is what the sign-up screen renders from: nothing of its own, and
// the chrome from the embedded Page -- which is where the messages arrive.
type signupData struct {
	view.Page
}

func init() {
	// What `aru view:build` emits for a screen whose boxes ask for
	// .OldValue(...) and .FieldError(...). No directive is involved: they are
	// promoted methods on the embedded view.Page.
	view.Register("flashe2e/signup", func(w io.Writer, data any) error {
		d, ok := data.(signupData)
		if !ok {
			return view.WrongData("flashe2e/signup", "signupData", data)
		}
		var b strings.Builder
		b.WriteString(`<form method="post" action="/signup">`)
		b.WriteString(`<input name="email" value="` + d.OldValue("email") + `">`)
		b.WriteString(`<p class="error">` + d.FieldError("email") + `</p>`)
		b.WriteString(`<input type="password" name="password" value="` + d.OldValue("password") + `">`)
		b.WriteString(`<p class="error">` + d.FieldError("password") + `</p>`)
		b.WriteString(`</form>`)
		_, err := io.WriteString(w, b.String())
		return err
	})
}

// storeSignup is the rule set, compiled once at boot in a package-level var --
// which is what makes a misspelled rule a failure at start rather than on the
// request that first exercises it.
var storeSignup = validation.MustCompile(validation.Rules{
	"email":    "required|email",
	"password": "required|min:12",
})

// signupModule is the application: one screen, one controller, two routes.
type signupModule struct{}

func (signupModule) Name() string { return "flashe2e" }

func (signupModule) Routes(r *fhttp.Router) {
	r.Action(http.MethodGet, "/signup", func(ctx *fhttp.Context) error {
		// The whole of what a controller writes about errors: nothing. The
		// constructor fills them, because the alternative is a line a handler
		// can forget and a form that comes back blank when it does.
		return ctx.View("flashe2e/signup", signupData{Page: view.New(ctx, "Sign up")})
	})

	r.Action(http.MethodPost, "/signup", func(ctx *fhttp.Context) error {
		if err := ctx.Request.ParseForm(); err != nil {
			return err
		}
		in, errs := storeSignup.Validate(ctx.Request.PostForm)
		if errs.Any() {
			// The whole of the failure path. It writes nothing and renders
			// nothing: the router recognises the error and answers it.
			//
			// The set is asked directly rather than through a Context.Validate
			// that read the form and applied the set in one call: that method
			// was removed as the second way to validate. This is the one way,
			// and the way `aru make:module` generates.
			return errs
		}
		// Only what the set declares is readable, and it is read by name.
		if in.String("email") == "" {
			return errors.New("a field that passed `required` came back empty")
		}
		return ctx.Redirect("/")
	})
}

// TestAPasswordOneCharacterShortComesBackOnTheScreen is the reported failure,
// asserted on the rendered HTML of the page the person lands on.
func TestAPasswordOneCharacterShortComesBackOnTheScreen(t *testing.T) {
	handler := signupApp(t)
	jar := cookieJar{}

	// The submission. A password one character short of the rule, and an address
	// that is fine -- so the test proves the message lands on the field that
	// failed and not on the one that did not.
	form := strings.NewReader("email=ada%40example.test&password=hunter2hunt")
	post := httptest.NewRequest(http.MethodPost, "http://example.test/signup", form)
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.Header.Set("Referer", "http://example.test/signup")
	rejected := httptest.NewRecorder()
	handler.ServeHTTP(rejected, post)

	if rejected.Code != http.StatusSeeOther {
		t.Fatalf("a rejected form answered %d, want %d -- a body is what nobody sees",
			rejected.Code, http.StatusSeeOther)
	}
	if to := rejected.Header().Get("Location"); to != "/signup" {
		t.Fatalf("sent back to %q, want %q", to, "/signup")
	}
	jar.take(rejected)

	// The navigation the browser makes next. Nothing about it mentions the
	// rejection: it is an ordinary GET of the form's own address.
	get := httptest.NewRequest(http.MethodGet, "http://example.test/signup", nil)
	get.Header.Set("Accept", "text/html,application/xhtml+xml")
	jar.put(get)
	landed := httptest.NewRecorder()
	handler.ServeHTTP(landed, get)

	page := landed.Body.String()
	if landed.Code != http.StatusOK {
		t.Fatalf("the page answered %d: %s", landed.Code, page)
	}

	// The deliverable.
	if !strings.Contains(page, "must be at least 12 characters") {
		t.Errorf("the page the person landed on does not say why the form was rejected:\n%s", page)
	}
	// What was typed comes back, so the form is not retyped from the start.
	if !strings.Contains(page, `value="ada@example.test"`) {
		t.Errorf("the address that was typed is not back in the box:\n%s", page)
	}
	// And the password never is.
	if strings.Contains(page, "hunter2hunt") {
		t.Errorf("the password came back in the markup:\n%s", page)
	}
	// The field that passed says nothing. A form that decorates every box the
	// moment one of them fails is one people stop reading.
	if strings.Contains(page, "is not a valid email address") {
		t.Errorf("a message landed on the field that was accepted:\n%s", page)
	}

	// Exactly one request. The browser is told to drop the cookie on the read,
	// so the reload that follows shows a clean form -- a message that outlives
	// its redirect appears on a page nobody submitted.
	jar.take(landed)
	again := httptest.NewRequest(http.MethodGet, "http://example.test/signup", nil)
	again.Header.Set("Accept", "text/html")
	jar.put(again)
	reloaded := httptest.NewRecorder()
	handler.ServeHTTP(reloaded, again)

	if strings.Contains(reloaded.Body.String(), "must be at least 12 characters") {
		t.Errorf("the message survived to a second page view:\n%s", reloaded.Body.String())
	}
}

// TestARejectedFormUnderHTMXLandsOnTheSameScreen is the same journey made by the
// client that made the original bug invisible.
//
// htmx does not swap a 4xx, so the 422 that used to answer here was fetched,
// discarded, and never seen. HX-Redirect makes the browser navigate, and the
// navigation is an ordinary page request -- so the message arrives by the same
// route as for a client with no JavaScript at all, which is the point of
// answering a rejection with a redirect rather than with a body.
func TestARejectedFormUnderHTMXLandsOnTheSameScreen(t *testing.T) {
	handler := signupApp(t)
	jar := cookieJar{}

	form := strings.NewReader("email=ada%40example.test&password=short")
	post := httptest.NewRequest(http.MethodPost, "http://example.test/signup", form)
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.Header.Set("Referer", "http://example.test/signup")
	post.Header.Set("HX-Request", "true")
	rejected := httptest.NewRecorder()
	handler.ServeHTTP(rejected, post)

	if to := rejected.Header().Get("HX-Redirect"); to != "/signup" {
		t.Fatalf("HX-Redirect = %q, want %q -- htmx would have thrown the answer away", to, "/signup")
	}
	if rejected.Code != http.StatusNoContent {
		t.Fatalf("answered %d, want %d: a body alongside HX-Redirect is swapped in before the browser navigates",
			rejected.Code, http.StatusNoContent)
	}
	jar.take(rejected)

	get := httptest.NewRequest(http.MethodGet, "http://example.test/signup", nil)
	get.Header.Set("Accept", "text/html")
	jar.put(get)
	landed := httptest.NewRecorder()
	handler.ServeHTTP(landed, get)

	if !strings.Contains(landed.Body.String(), "must be at least 12 characters") {
		t.Errorf("the HTMX journey ended on a page with no message on it:\n%s", landed.Body.String())
	}
}

// TestTheFlashIsNotSpentByTheFragmentTheLandingPageFires is the failure mode of
// consuming the flash on anything that asks.
//
// A page fires its own hx-get on arrival -- a table, a counter, a notification
// poll. If that request spent the one-shot flash, the page underneath it would
// render with nothing on it, and the bug would come back in a form that only
// appears on screens busy enough to have a fragment.
func TestTheFlashIsNotSpentByTheFragmentTheLandingPageFires(t *testing.T) {
	handler := signupApp(t)
	jar := cookieJar{}

	form := strings.NewReader("email=ada%40example.test&password=short")
	post := httptest.NewRequest(http.MethodPost, "http://example.test/signup", form)
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.Header.Set("Referer", "http://example.test/signup")
	rejected := httptest.NewRecorder()
	handler.ServeHTTP(rejected, post)
	jar.take(rejected)

	// What XHR sends: */*, not text/html.
	fragment := httptest.NewRequest(http.MethodGet, "http://example.test/signup", nil)
	fragment.Header.Set("Accept", "*/*")
	fragment.Header.Set("HX-Request", "true")
	jar.put(fragment)
	swap := httptest.NewRecorder()
	handler.ServeHTTP(swap, fragment)
	jar.take(swap)

	page := httptest.NewRequest(http.MethodGet, "http://example.test/signup", nil)
	page.Header.Set("Accept", "text/html")
	jar.put(page)
	landed := httptest.NewRecorder()
	handler.ServeHTTP(landed, page)

	if !strings.Contains(landed.Body.String(), "must be at least 12 characters") {
		t.Errorf("a fragment spent the flash and the page rendered without it:\n%s", landed.Body.String())
	}
}

// signupApp boots the framework the way an application does: the kernel, the
// view module, and a module with routes. Nothing here wires the flash -- that is
// what is being tested.
func signupApp(t *testing.T) http.Handler {
	t.Helper()

	k := kernel.New(bootstrap.Configuration{
		App: config.App{
			Name:     "test",
			Env:      config.EnvProd,
			HTTPAddr: ":0",
			Key:      []byte(strings.Repeat("k", encryption.KeySize)),
		},
	}).Register(view.NewModule(), signupModule{})

	if err := k.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	return k.Handler()
}

// cookieJar is the half of a browser this test needs: it keeps what a response
// set and sends it back, and forgets what a response cleared.
//
// Forgetting is the part that matters. "Exactly one request" is a property of
// the framework telling the browser to drop the cookie, and a test that replayed
// the cookie regardless would pass whether or not that instruction was ever
// sent.
type cookieJar map[string]string

func (j cookieJar) take(rec *httptest.ResponseRecorder) {
	for _, c := range (&http.Response{Header: rec.Header()}).Cookies() {
		if c.MaxAge < 0 || c.Value == "" {
			delete(j, c.Name)
			continue
		}
		j[c.Name] = c.Value
	}
}

func (j cookieJar) put(r *http.Request) {
	for name, value := range j {
		r.AddCookie(&http.Cookie{Name: name, Value: value})
	}
}

// TestTheFlashCookieIsTheOnlyThingCarryingIt pins the mechanism the three tests
// above depend on, so that a failure in them says which half broke.
func TestTheFlashCookieIsTheOnlyThingCarryingIt(t *testing.T) {
	handler := signupApp(t)

	form := strings.NewReader("email=ada%40example.test&password=short")
	post := httptest.NewRequest(http.MethodPost, "http://example.test/signup", form)
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.Header.Set("Referer", "http://example.test/signup")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, post)

	var flash *http.Cookie
	for _, c := range (&http.Response{Header: rec.Header()}).Cookies() {
		if c.Name == security.FlashCookieName {
			flash = c
		}
	}
	if flash == nil {
		t.Fatalf("a rejected form set no %s cookie", security.FlashCookieName)
	}
	if !flash.HttpOnly {
		t.Error("the flash cookie is readable from script: it carries what somebody typed")
	}
	if len(flash.Value) > security.MaxFlashBytes {
		t.Errorf("the flash cookie is %d bytes, over the %d budget: a browser drops it silently",
			len(flash.Value), security.MaxFlashBytes)
	}
	if strings.Contains(flash.Value, "short") {
		t.Error("the password is in the cookie value")
	}
}
