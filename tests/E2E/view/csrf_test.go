//go:build e2e

package e2e

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/framework/foundation/bootstrap"
	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/http/middleware"
	"github.com/arandu-io/framework/kernel"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/framework/validation"
	"github.com/arandu-io/framework/view"
	"github.com/arandu-io/hesape/config"
	"github.com/arandu-io/hesape/encryption"
	"github.com/arandu-io/hesape/session"
)

// The sign-in form is the one a visitor submits before there is any session to
// bind a token to. These tests walk it the way a browser does -- a page, a
// cookie kept, a form posted back -- through a real kernel with CSRFProtect in
// the pipeline, and the controller never touches the token: view.New is the
// only thing that puts it on the page.

// signinData is what the sign-in screen renders from.
type signinData struct {
	view.Page
}

func init() {
	view.Register("csrfe2e/signin", func(w io.Writer, data any) error {
		d, ok := data.(signinData)
		if !ok {
			return view.WrongData("csrfe2e/signin", "signinData", data)
		}
		_, err := io.WriteString(w, `<form method="post" action="/signin">`+
			`<input type="hidden" name="_token" value="`+d.CSRFToken()+`">`+
			`<p class="error">`+d.FieldError("email")+`</p></form>`)
		return err
	})
}

// signinModule draws the form on GET, and on POST either accepts it or draws
// it again in place, the way a refused sign-in is answered.
type signinModule struct{}

func (signinModule) Name() string { return "csrfe2e" }

func (signinModule) Routes(r *fhttp.Router) {
	r.Action(http.MethodGet, "/signin", func(ctx *fhttp.Context) error {
		return ctx.View("csrfe2e/signin", signinData{Page: view.New(ctx, "Sign in")})
	})
	r.Action(http.MethodPost, "/signin", func(ctx *fhttp.Context) error {
		if err := ctx.Request.ParseForm(); err != nil {
			return err
		}
		if ctx.Request.PostForm.Get("email") == "" {
			page := view.New(ctx, "Sign in")
			page.Errors = validation.Errors{"email": {"required"}}
			return ctx.Fragment(http.StatusUnprocessableEntity, "csrfe2e/signin", signinData{Page: page})
		}
		_, err := io.WriteString(ctx.Response, "signed in")
		return err
	})
}

// signinApp boots the kernel with CSRFProtect in front of the module, bound to
// a real session store, and returns the store so a test can sign somebody in.
func signinApp(t *testing.T) (http.Handler, *security.SessionStore) {
	t.Helper()
	key := []byte(strings.Repeat("k", encryption.KeySize))
	sessions := security.NewSessionStore(key, time.Hour, false, security.NewMemoryBackend())
	csrf := security.NewCSRF(key, time.Hour)

	k := kernel.New(bootstrap.Configuration{
		App: config.App{Name: "test", Env: config.EnvProd, HTTPAddr: ":0", Key: key},
	}).Register(view.NewModule(), signinModule{}).
		Use(middleware.CSRFProtect(csrf, sessions.IDFromRequest))

	if err := k.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	return k.Handler(), sessions
}

var tokenValue = regexp.MustCompile(`name="_token" value="([^"]*)"`)

// openSignin loads the sign-in page with jar, keeps what it set, and returns
// the token the page carries.
func openSignin(t *testing.T, h http.Handler, jar cookieJar) string {
	t.Helper()
	get := httptest.NewRequest(http.MethodGet, "http://example.test/signin", nil)
	get.Header.Set("Accept", "text/html")
	jar.put(get)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, get)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /signin answered %d: %s", rec.Code, rec.Body.String())
	}
	jar.take(rec)
	m := tokenValue.FindStringSubmatch(rec.Body.String())
	if m == nil || m[1] == "" {
		t.Fatalf("the page carries no token, so view.New did not see the one the middleware issued:\n%s", rec.Body.String())
	}
	return m[1]
}

// postSignin submits the form with token and email, sending jar's cookies.
func postSignin(h http.Handler, jar cookieJar, token, email string) *httptest.ResponseRecorder {
	body := url.Values{"_token": {token}, "email": {email}}.Encode()
	post := httptest.NewRequest(http.MethodPost, "http://example.test/signin", strings.NewReader(body))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	jar.put(post)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, post)
	jar.take(rec)
	return rec
}

// TestAGuestSignsInWithTheTokenTheirPageCarried is the whole journey for
// somebody with no session: the page sets the guest cookie, and the token on it
// validates when the same browser posts it back.
func TestAGuestSignsInWithTheTokenTheirPageCarried(t *testing.T) {
	h, _ := signinApp(t)
	jar := cookieJar{}

	token := openSignin(t, h, jar)
	if jar[session.GuestCookieName] == "" {
		t.Fatalf("the sign-in page set no %s cookie, so the token is bound to nothing", session.GuestCookieName)
	}

	if rec := postSignin(h, jar, token, "ada@example.test"); rec.Code != http.StatusOK {
		t.Fatalf("the guest's own token was refused with %d: %s", rec.Code, rec.Body.String())
	}
}

// TestAGuestTokenIsRefusedFromAnotherBrowser is what binding to the guest
// cookie buys: the token a page carried used to be bound to the empty id, and
// passed on every visitor's form alike.
func TestAGuestTokenIsRefusedFromAnotherBrowser(t *testing.T) {
	h, _ := signinApp(t)

	victim := cookieJar{}
	token := openSignin(t, h, victim)

	attacker := cookieJar{}
	_ = openSignin(t, h, attacker)
	if rec := postSignin(h, attacker, token, "ada@example.test"); rec.Code != middleware.StatusCSRFExpired {
		t.Fatalf("another browser's guest token answered %d, want %d", rec.Code, middleware.StatusCSRFExpired)
	}

	if rec := postSignin(h, cookieJar{}, token, "ada@example.test"); rec.Code != middleware.StatusCSRFExpired {
		t.Fatalf("a token posted with no cookie at all answered %d, want %d", rec.Code, middleware.StatusCSRFExpired)
	}
}

// TestAFormRedrawnInPlaceCarriesATokenThatStillValidates: a refused sign-in is
// answered with the form, on the POST itself, and the token on it has to work
// for the next attempt.
func TestAFormRedrawnInPlaceCarriesATokenThatStillValidates(t *testing.T) {
	h, _ := signinApp(t)
	jar := cookieJar{}
	token := openSignin(t, h, jar)

	refused := postSignin(h, jar, token, "")
	if refused.Code != http.StatusUnprocessableEntity {
		t.Fatalf("an empty email answered %d, want 422: %s", refused.Code, refused.Body.String())
	}
	m := tokenValue.FindStringSubmatch(refused.Body.String())
	if m == nil || m[1] == "" {
		t.Fatalf("the form drawn again on the POST carries no token:\n%s", refused.Body.String())
	}

	if rec := postSignin(h, jar, m[1], "ada@example.test"); rec.Code != http.StatusOK {
		t.Fatalf("the redrawn form's token was refused with %d", rec.Code)
	}
}

// TestASignedInSessionIsBoundToItsSessionAndGetsNoGuestCookie: with a session,
// the token is bound to its id, and a token from another session is refused.
func TestASignedInSessionIsBoundToItsSessionAndGetsNoGuestCookie(t *testing.T) {
	h, sessions := signinApp(t)

	signIn := func() cookieJar {
		rec := httptest.NewRecorder()
		if _, err := sessions.Start(context.Background(), rec, security.Subject{ID: "u1", Tenant: "t1"}); err != nil {
			t.Fatalf("Start: %v", err)
		}
		jar := cookieJar{}
		jar.take(rec)
		return jar
	}

	first := signIn()
	token := openSignin(t, h, first)
	if _, set := first[session.GuestCookieName]; set {
		t.Errorf("a signed-in request was given a %s cookie", session.GuestCookieName)
	}
	if rec := postSignin(h, first, token, "ada@example.test"); rec.Code != http.StatusOK {
		t.Fatalf("the session's own token was refused with %d", rec.Code)
	}

	second := signIn()
	if rec := postSignin(h, second, token, "ada@example.test"); rec.Code != middleware.StatusCSRFExpired {
		t.Fatalf("another session's token answered %d, want %d", rec.Code, middleware.StatusCSRFExpired)
	}
}
