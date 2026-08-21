package unit

import (
	"log/slog"
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
// Reading it as seconds compiles, boots, and turns an existing
// SESSION_LIFETIME=120 into a two-minute session. Everybody stays signed in long
// enough for it to look like it worked and is then thrown out mid-form.
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
// The CSRF token is bound to the session, and a cookie name set independently
// breaks the binding with no error anywhere -- forms start answering 419 and
// nothing says why.
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

// A file is customer data, so a disk defaults to private and never to public.
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
// be what the struct holds. Two sources that can disagree is what the typed
// struct exists to prevent.
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

// The root logger has a level of its own, and LOG_LEVEL is where it comes from.
//
// A channel carries its own level under Log.Channels. The root has no channel to
// inherit one from, so the same variable answers both, parsed once into the type
// the logger takes.
func TestTheRootLogLevelComesFromTheSameVariableAsTheChannels(t *testing.T) {
	env(t, "APP_KEY", testKey, "LOG_LEVEL", "error")

	cfg, err := bootstrap.LoadConfiguration()
	if err != nil {
		t.Fatalf("LoadConfiguration: %v", err)
	}

	if got := cfg.Observability.LogLevel; got != slog.LevelError {
		t.Errorf("the root level is %v, want %v", got, slog.LevelError)
	}
	if got := cfg.Log.Channels["single"].Level; got != "error" {
		t.Errorf("the channel level is %q, want error -- the two read one variable", got)
	}
}

// A typo in LOG_LEVEL stops the process instead of restoring a default.
//
// The level a channel falls back to is debug, so a misspelt name would turn a
// deployment that asked for "error" into one writing every bound argument it
// sees, and nothing would say so.
func TestAnUnknownLogLevelFailsTheBoot(t *testing.T) {
	env(t, "APP_KEY", testKey, "LOG_LEVEL", "warn")

	if _, err := bootstrap.LoadConfiguration(); err == nil {
		t.Fatal("LOG_LEVEL=warn was accepted; it is not one of the eight names and falls back to debug")
	}
}

// Debug logging in production leaks the request into the log, and the refusal
// belongs at boot rather than at the first query written out.
func TestDebugLoggingIsRefusedInProduction(t *testing.T) {
	env(t, "APP_KEY", testKey, "APP_ENV", "prod", "APP_DEBUG", "false",
		"APP_URL", "https://loja.example", "LOG_LEVEL", "debug")

	if _, err := bootstrap.LoadConfiguration(); err == nil {
		t.Fatal("LOG_LEVEL=debug was accepted in production")
	}
}

// The console gate is off unless a deployment turns it on. An empty secret is
// the zero value, and treating it as "no gate" would open the buffer of every
// application that never set one.
func TestTracingIsOptInPerDeployment(t *testing.T) {
	env(t, "APP_KEY", testKey)

	cfg, err := bootstrap.LoadConfiguration()
	if err != nil {
		t.Fatalf("LoadConfiguration: %v", err)
	}
	if cfg.Observability.TracingSecret != "" {
		t.Error("a tracing secret appeared without one being configured")
	}

	t.Setenv("ARANDU_TRACING_SECRET", "s3cret-operator-only")
	cfg, err = bootstrap.LoadConfiguration()
	if err != nil {
		t.Fatalf("LoadConfiguration: %v", err)
	}
	if cfg.Observability.TracingSecret != "s3cret-operator-only" {
		t.Errorf("the secret is %q", cfg.Observability.TracingSecret)
	}
}

// ARANDU_EDITOR has one reader, and the exception handler is handed what the
// Configuration holds rather than reading it again.
//
// Two readers of one variable are two answers the day one of them grows a
// fallback the other does not: the error page would link to one editor and the
// debug console to another, from the same .env.
func TestTheEditorIsReadOnceAndHandedOn(t *testing.T) {
	env(t, "APP_KEY", testKey)

	cfg, err := bootstrap.LoadConfiguration()
	if err != nil {
		t.Fatalf("LoadConfiguration: %v", err)
	}
	if got := cfg.Observability.Editor; got != "vscode" {
		t.Errorf("the default editor is %q, want vscode", got)
	}

	t.Setenv("ARANDU_EDITOR", "goland")
	cfg, err = bootstrap.LoadConfiguration()
	if err != nil {
		t.Fatalf("LoadConfiguration: %v", err)
	}
	if got := cfg.Observability.Editor; got != "goland" {
		t.Errorf("the editor is %q, want goland", got)
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

// AfterCommit stays false, and the reason is the outbox.
//
// The outbox writes the event in the same transaction as the change that
// produced it, so the window AfterCommit narrows is one the events path does
// not have at all. Turning it on here would be a second, weaker answer to a
// problem already solved.
func TestTheDatabaseQueueDoesNotDispatchAfterCommit(t *testing.T) {
	env(t, "APP_KEY", testKey)

	cfg, err := bootstrap.LoadConfiguration()
	if err != nil {
		t.Fatalf("LoadConfiguration: %v", err)
	}

	if cfg.Queue.Connections["database"].AfterCommit {
		t.Error("AfterCommit is on; the outbox closes that window and this only narrows it")
	}
}

// The reload script follows debug and has no variable of its own. Serving it
// outside development costs a request per page for something nobody there can
// use.
func TestTheReloadScriptFollowsDebugAndNothingElse(t *testing.T) {
	env(t, "APP_KEY", testKey, "APP_DEBUG", "false")

	cfg, err := bootstrap.LoadConfiguration()
	if err != nil {
		t.Fatalf("LoadConfiguration: %v", err)
	}
	if cfg.View.Reload {
		t.Error("the reload script is on with debug off")
	}
	if !cfg.View.Fragments {
		t.Error("fragments are off; every HTMX swap would re-render the chrome around the part that changed")
	}
}
