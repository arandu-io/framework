package bootstrap_test

import (
	"os"
	"testing"
	"time"

	"github.com/arandu-io/framework/foundation/bootstrap"
)

// A key is required, and every test here needs one that passes validation.
const testKey = "base64:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

func env(t *testing.T, pairs ...string) {
	t.Helper()
	for i := 0; i < len(pairs); i += 2 {
		t.Setenv(pairs[i], pairs[i+1])
	}
}

func TestLoadConfigurationRefusesToBootWithoutAKey(t *testing.T) {
	t.Setenv("APP_KEY", "")

	if _, err := bootstrap.LoadConfiguration(); err == nil {
		t.Fatal("booted without an application key; the failure has to arrive at start, not on the first request that signs a cookie")
	}
}

// SESSION_LIFETIME is minutes, and this is the test that says so.
//
// Reading it as seconds compiles, boots, and turns a .env carried over from
// Laravel into a two-minute session. Everybody stays signed in long enough for
// it to look like it worked and is then thrown out mid-form.
func TestSessionLifetimeIsReadAsMinutesLikeLaravel(t *testing.T) {
	env(t, "APP_KEY", testKey, "SESSION_LIFETIME", "120")

	cfg, err := bootstrap.LoadConfiguration()
	if err != nil {
		t.Fatalf("LoadConfiguration: %v", err)
	}

	if got, want := cfg.Session.Lifetime, 2*time.Hour; got != want {
		t.Errorf("SESSION_LIFETIME=120 became %v, want %v -- it is minutes, as Laravel writes it", got, want)
	}
}

// The cookie is not configurable on its own, and this pins it.
//
// hesape/session documents the reason: the CSRF token is bound to the session,
// and a cookie name set independently breaks the binding with no error anywhere
// -- forms start answering 419 and nothing says why.
func TestTheSessionCookieFollowsTheApplicationNameAndNothingElse(t *testing.T) {
	env(t, "APP_KEY", testKey, "APP_NAME", "Loja Grande", "SESSION_COOKIE", "somethingelse")

	cfg, err := bootstrap.LoadConfiguration()
	if err != nil {
		t.Fatalf("LoadConfiguration: %v", err)
	}

	if got, want := cfg.Session.Cookie, "loja_grande_session"; got != want {
		t.Errorf("cookie is %q, want %q -- SESSION_COOKIE must not be a way to set it", got, want)
	}
}

// A cookie that travels in the clear because nobody set a variable is the
// failure that looks like nothing at all, so the default follows the URL.
func TestTheSessionCookieIsSecureWhenTheApplicationIsHTTPS(t *testing.T) {
	env(t, "APP_KEY", testKey, "APP_URL", "https://loja.example")

	cfg, err := bootstrap.LoadConfiguration()
	if err != nil {
		t.Fatalf("LoadConfiguration: %v", err)
	}
	if !cfg.Session.Secure {
		t.Error("an https application got an insecure session cookie by default")
	}

	t.Setenv("APP_URL", "http://localhost:8080")
	cfg, err = bootstrap.LoadConfiguration()
	if err != nil {
		t.Fatalf("LoadConfiguration: %v", err)
	}
	if cfg.Session.Secure {
		t.Error("a plain-http application got a secure cookie, which never arrives")
	}
}

// A file is customer data. Laravel defaults a disk to public; this does not.
func TestTheDefaultDiskIsPrivate(t *testing.T) {
	env(t, "APP_KEY", testKey)

	cfg, err := bootstrap.LoadConfiguration()
	if err != nil {
		t.Fatalf("LoadConfiguration: %v", err)
	}

	if got := cfg.Filesystem.Visibility; got != "private" {
		t.Errorf("the default disk visibility is %q, want private -- a path anybody can guess is a leak with a directory name (RULE 14)", got)
	}
}

// The environment wins over the file, never the other way round: a deploy sets
// a variable, and a stale .env in the image must not beat it.
func TestTheEnvironmentWinsOverTheDotenvFile(t *testing.T) {
	dir := t.TempDir()
	if err := writeFile(dir+"/.env", "APP_NAME=fromfile\n"); err != nil {
		t.Fatal(err)
	}
	chdir(t, dir)

	env(t, "APP_KEY", testKey, "APP_NAME", "fromenv")

	cfg, err := bootstrap.LoadConfiguration()
	if err != nil {
		t.Fatalf("LoadConfiguration: %v", err)
	}
	if cfg.App.Name != "fromenv" {
		t.Errorf("the file won over the environment: got %q", cfg.App.Name)
	}
}

// The Repository is a reader over the same settings, so what it answers has to
// be what the struct holds. Two sources that can disagree is the thing ADR 0051
// exists to prevent.
func TestTheRepositoryAnswersWhatTheStructsHold(t *testing.T) {
	env(t, "APP_KEY", testKey, "APP_NAME", "loja", "CACHE_STORE", "array")

	cfg, err := bootstrap.LoadConfiguration()
	if err != nil {
		t.Fatalf("LoadConfiguration: %v", err)
	}

	if got, _ := cfg.Repository.String("app.name"); got != cfg.App.Name {
		t.Errorf("repository says app.name is %q, struct says %q", got, cfg.App.Name)
	}
	if got, _ := cfg.Repository.String("cache.default"); got != cfg.Cache.Default {
		t.Errorf("repository says cache.default is %q, struct says %q", got, cfg.Cache.Default)
	}
}

// argon2id, not bcrypt. There are no previous Arandu hashes to be compatible
// with, and argon2id is what the one third-party dependency is taken for.
func TestTheDefaultHashDriverIsArgon2id(t *testing.T) {
	env(t, "APP_KEY", testKey)

	cfg, err := bootstrap.LoadConfiguration()
	if err != nil {
		t.Fatalf("LoadConfiguration: %v", err)
	}

	if got, _ := cfg.Repository.String("hashing.driver"); got != "argon2id" {
		t.Errorf("the default hash driver is %q, want argon2id", got)
	}
}

func writeFile(path, body string) error { return os.WriteFile(path, []byte(body), 0o600) }

func chdir(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
}
