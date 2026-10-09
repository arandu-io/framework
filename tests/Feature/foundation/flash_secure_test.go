package feature

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/arandu-io/framework/foundation"
	"github.com/arandu-io/framework/foundation/bootstrap"
	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/config"
)

// flashSecureKey passes validation; every case here needs one.
const flashSecureKey = "base64:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

// routerFlashModule is a module that writes a notice of its own, the way an
// authentication screen does after a form succeeded, with the flash the router
// handed it.
type routerFlashModule struct {
	flashModule
	got chan *security.Flash
}

func (m routerFlashModule) Routes(r *fhttp.Router) {
	m.got <- r.Flash()
	m.flashModule.Routes(r)
}

// flashCookie returns the flash cookie a response set, or nil.
func flashCookie(h http.Header) *http.Cookie {
	for _, c := range (&http.Response{Header: h}).Cookies() {
		if c.Name == security.FlashCookieName {
			return c
		}
	}
	return nil
}

// rejectedFlash boots the application cfg describes, posts a form it rejects,
// and returns the flash cookie that came back.
func rejectedFlash(t *testing.T, cfg bootstrap.Configuration) *http.Cookie {
	t.Helper()
	k := foundation.New(cfg).Register(flashModule{seen: make(chan fhttp.State, 1)})
	if err := k.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	post := httptest.NewRequest(http.MethodPost, "http://example.test/posts", strings.NewReader("title="))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.Header.Set("Referer", "http://example.test/posts/new")
	rec := httptest.NewRecorder()
	k.Handler().ServeHTTP(rec, post)

	c := flashCookie(rec.Header())
	if c == nil {
		t.Fatalf("no flash cookie on the rejection (status %d)", rec.Code)
	}
	return c
}

// TestTheFlashCookieTakesSecureFromTheSessionConfiguration is the property the
// kernel's flash exists under: one decision of Secure, read once, for both the
// session cookie and the flash.
//
// Each case is a configuration read from the environment, the way a process
// reads it, so what is compared is the decision the loader made and not a
// value the test wrote into a struct. The default is Secure outside dev
// whatever APP_URL says, and SESSION_SECURE_COOKIE decides when it is set.
func TestTheFlashCookieTakesSecureFromTheSessionConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  []string
		want bool
	}{
		{"prod over https", []string{"APP_ENV", "prod", "APP_URL", "https://loja.example"}, true},
		{"staging over http without the variable", []string{"APP_ENV", "staging", "APP_URL", "http://loja.internal"}, true},
		{"prod with SESSION_SECURE_COOKIE=false", []string{"APP_ENV", "prod", "APP_URL", "https://loja.example", "SESSION_SECURE_COOKIE", "false"}, false},
		{"dev over http", []string{"APP_ENV", "dev", "APP_URL", "http://localhost:8080"}, false},
		{"dev with an https URL", []string{"APP_ENV", "dev", "APP_URL", "https://loja.test"}, false},
		{"dev with SESSION_SECURE_COOKIE=true", []string{"APP_ENV", "dev", "APP_URL", "http://localhost:8080", "SESSION_SECURE_COOKIE", "true"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("APP_KEY", flashSecureKey)
			t.Setenv("SESSION_SECURE_COOKIE", "")
			for i := 0; i < len(tc.env); i += 2 {
				t.Setenv(tc.env[i], tc.env[i+1])
			}
			cfg, err := bootstrap.LoadConfiguration()
			if err != nil {
				t.Fatalf("LoadConfiguration: %v", err)
			}
			if cfg.Session.Secure != tc.want {
				t.Fatalf("session Secure = %v, want %v: the loader changed, not the flash", cfg.Session.Secure, tc.want)
			}

			if got := rejectedFlash(t, cfg).Secure; got != cfg.Session.Secure {
				t.Errorf("flash Secure = %v, session Secure = %v: the two cookies disagree", got, cfg.Session.Secure)
			}
		})
	}
}

// TestAModuleWritesWithTheRoutersFlash is why Router.Flash exists: a module
// that writes a notice of its own reaches the flash the kernel built instead
// of building a second one, so the notice is read back by the middleware the
// kernel installed and carries the session's Secure.
func TestAModuleWritesWithTheRoutersFlash(t *testing.T) {
	for _, secure := range []bool{true, false} {
		cfg := testConfig(config.EnvProd)
		cfg.Session.Secure = secure
		seen := make(chan fhttp.State, 1)
		got := make(chan *security.Flash, 1)
		k := foundation.New(cfg).Register(routerFlashModule{flashModule: flashModule{seen: seen}, got: got})
		if err := k.Boot(context.Background()); err != nil {
			t.Fatalf("Boot: %v", err)
		}
		f := <-got
		if f == nil {
			t.Fatal("the router the kernel handed the module carries no flash")
		}

		rec := httptest.NewRecorder()
		f.Write(rec, map[string][]string{"status": {"saved"}}, url.Values{})
		c := flashCookie(rec.Header())
		if c == nil {
			t.Fatal("the router's flash wrote no cookie")
		}
		if c.Secure != secure {
			t.Errorf("session Secure = %v, the router's flash wrote Secure = %v", secure, c.Secure)
		}

		page := httptest.NewRequest(http.MethodGet, "http://example.test/posts/new", nil)
		page.Header.Set("Accept", "text/html")
		page.AddCookie(c)
		k.Handler().ServeHTTP(httptest.NewRecorder(), page)
		if state := <-seen; len(state.Errors["status"]) != 1 {
			t.Errorf("the page was given %v: the kernel did not read back what the router's flash wrote", state.Errors)
		}
	}
}
