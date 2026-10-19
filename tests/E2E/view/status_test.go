//go:build e2e

package e2e

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/arandu-io/framework/foundation/bootstrap"
	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/kernel"
	"github.com/arandu-io/framework/view"
	"github.com/arandu-io/hesape/config"
	"github.com/arandu-io/hesape/encryption"
	hhttp "github.com/arandu-io/hesape/http"
)

// The other half of answering a form: one that worked redirects to the page
// that shows the result, and that page says what happened -- once.

type profileData struct {
	view.Page
}

func init() {
	view.Register("statuse2e/profile", func(w io.Writer, data any) error {
		d, ok := data.(profileData)
		if !ok {
			return view.WrongData("statuse2e/profile", "profileData", data)
		}
		_, err := io.WriteString(w, `<p class="status">`+d.Messages[hhttp.StatusKey]+`</p><form method="post" action="/profile"></form>`)
		return err
	})
}

type profileModule struct{}

func (profileModule) Name() string { return "statuse2e" }

func (profileModule) Routes(r *fhttp.Router) {
	r.Action(http.MethodGet, "/profile", func(ctx *fhttp.Context) error {
		return ctx.View("statuse2e/profile", profileData{Page: view.New(ctx, "Profile")})
	})
	r.Action(http.MethodPost, "/profile", func(ctx *fhttp.Context) error {
		return ctx.With(hhttp.StatusKey, "Perfil salvo.").Redirect("/profile")
	})
}

func profileApp(t *testing.T) http.Handler {
	t.Helper()
	k := kernel.New(bootstrap.Configuration{
		App: config.App{
			Name:     "test",
			Env:      config.EnvProd,
			HTTPAddr: ":0",
			Key:      []byte(strings.Repeat("k", encryption.KeySize)),
		},
	}).Register(view.NewModule(), profileModule{})
	if err := k.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	return k.Handler()
}

// TestAFormThatWorkedSaysSoOnThePageItLandsOn posts, follows the redirect the
// way a browser does, and reads the sentence on the page -- then reloads, and
// reads it gone.
func TestAFormThatWorkedSaysSoOnThePageItLandsOn(t *testing.T) {
	for _, htmx := range []bool{false, true} {
		handler := profileApp(t)
		jar := cookieJar{}

		post := httptest.NewRequest(http.MethodPost, "http://example.test/profile", strings.NewReader("name=Ada"))
		post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if htmx {
			post.Header.Set("HX-Request", "true")
		}
		saved := httptest.NewRecorder()
		handler.ServeHTTP(saved, post)

		to := saved.Header().Get("Location")
		if htmx {
			to = saved.Header().Get("HX-Redirect")
		}
		if to != "/profile" {
			t.Fatalf("htmx=%v: answered %d to %q, want a redirect to /profile", htmx, saved.Code, to)
		}
		jar.take(saved)

		get := httptest.NewRequest(http.MethodGet, "http://example.test/profile", nil)
		get.Header.Set("Accept", "text/html")
		jar.put(get)
		landed := httptest.NewRecorder()
		handler.ServeHTTP(landed, get)
		if !strings.Contains(landed.Body.String(), `<p class="status">Perfil salvo.</p>`) {
			t.Errorf("htmx=%v: the page the form landed on does not say it was saved:\n%s", htmx, landed.Body.String())
		}

		jar.take(landed)
		again := httptest.NewRequest(http.MethodGet, "http://example.test/profile", nil)
		again.Header.Set("Accept", "text/html")
		jar.put(again)
		reloaded := httptest.NewRecorder()
		handler.ServeHTTP(reloaded, again)
		if strings.Contains(reloaded.Body.String(), "Perfil salvo.") {
			t.Errorf("htmx=%v: the status survived to a second page view", htmx)
		}
	}
}
