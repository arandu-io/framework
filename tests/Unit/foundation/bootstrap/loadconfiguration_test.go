package unit

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
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
func TestSessionLifetimeIsReadAsMinutes(t *testing.T) {
	env(t, "APP_KEY", testKey, "SESSION_LIFETIME", "120")

	cfg, err := bootstrap.LoadConfiguration()
	if err != nil {
		t.Fatalf("LoadConfiguration: %v", err)
	}

	if got, want := cfg.Session.Lifetime, 2*time.Hour; got != want {
		t.Errorf("SESSION_LIFETIME=120 became %v, want %v -- the unit is minutes", got, want)
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

// There is no hash driver to publish, and that is the guarantee.
//
// This used to publish "argon2id" under hashing.driver and assert it, which
// defended the right thing by the weaker means: a default is a default, and a
// project could set another. The key is gone because the component behind it is
// gone -- one function, parameters compiled in, nothing to select. So the check
// is that the key is absent: a driver key coming back means somebody restored a
// choice, and a choice is how a project silently writes weaker hashes.
func TestNoHashDriverIsPublishedBecauseThereIsNothingToChoose(t *testing.T) {
	env(t, "APP_KEY", testKey)

	cfg, err := bootstrap.LoadConfiguration()
	if err != nil {
		t.Fatalf("LoadConfiguration: %v", err)
	}

	for _, key := range []string{"hashing.driver", "hashing.argon.memory", "hashing.argon.time", "hashing.argon.threads"} {
		if got, err := cfg.Repository.String(key); err == nil && got != "" {
			t.Errorf("%s is published as %q, and nothing reads it: the parameters are compiled in", key, got)
		}
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

// The three pool settings reach the Config.
//
// Until they were read they had nowhere to arrive: DATABASE_URL says where the
// database is and nothing about how many connections to hold, so an application
// that set these three got the defaults and no error saying the variables were
// ignored.
func TestThePoolSettingsReachTheDatabaseConfig(t *testing.T) {
	env(t, "APP_KEY", testKey,
		"DB_MAX_OPEN_CONNS", "50",
		"DB_MAX_IDLE_CONNS", "12",
		"DB_CONN_MAX_LIFETIME", "900")

	cfg, err := bootstrap.LoadConfiguration()
	if err != nil {
		t.Fatalf("LoadConfiguration: %v", err)
	}

	if got := cfg.Database.MaxOpenConns; got != 50 {
		t.Errorf("DB_MAX_OPEN_CONNS=50 arrived as %d", got)
	}
	if got := cfg.Database.MaxIdleConns; got != 12 {
		t.Errorf("DB_MAX_IDLE_CONNS=12 arrived as %d", got)
	}
	if got, want := cfg.Database.ConnMaxLifetime, 15*time.Minute; got != want {
		t.Errorf("DB_CONN_MAX_LIFETIME=900 arrived as %v, want %v -- the unit is seconds", got, want)
	}
}

// Unset leaves all three at zero, and the zero is the assertion.
//
// The database package reads a zero on any of these as the pool it keeps by
// default; database/sql's meaning for the same zero is an unbounded pool, which
// is what the bound exists to prevent. A number written here as well would be a
// second place to change one, so this asserts what this function produces --
// zero -- and not the 25, 5 and hour that zero turns into. Asserting those would
// be pinning another package's behaviour from the outside, and it would keep
// passing on the day this function started writing them itself.
func TestThePoolSettingsStayAtZeroWhenUnset(t *testing.T) {
	env(t, "APP_KEY", testKey)
	unset(t, "DB_MAX_OPEN_CONNS", "DB_MAX_IDLE_CONNS", "DB_CONN_MAX_LIFETIME")

	cfg, err := bootstrap.LoadConfiguration()
	if err != nil {
		t.Fatalf("LoadConfiguration: %v", err)
	}

	if got := cfg.Database.MaxOpenConns; got != 0 {
		t.Errorf("MaxOpenConns is %d with nothing set, want 0 -- zero is what the database package reads as its own default", got)
	}
	if got := cfg.Database.MaxIdleConns; got != 0 {
		t.Errorf("MaxIdleConns is %d with nothing set, want 0", got)
	}
	if got := cfg.Database.ConnMaxLifetime; got != 0 {
		t.Errorf("ConnMaxLifetime is %v with nothing set, want 0", got)
	}
}

// A value that is there and cannot be used stops the boot, and the message has
// to name the variable and quote what came.
//
// This is the only reader of these three variables in the collection now: the
// skeletons parsed them too, and theirs refused a value that did not parse.
// Falling back silently here would drop that refusal for everyone at once --
// the operator sets DB_MAX_OPEN_CONNS=fifty, gets zero, zero is read as the
// package default, and the pool is the default one while the .env says
// otherwise. Nothing logs it, because nothing knows.
//
// The message is asserted and not merely the error, because an error that does
// not name the variable sends somebody through six files looking for it.
func TestAPoolSettingThatCannotBeReadStopsTheBoot(t *testing.T) {
	for _, tc := range []struct {
		key, value string
		wants      []string
	}{
		{"DB_MAX_OPEN_CONNS", "fifty", []string{`DB_MAX_OPEN_CONNS is "fifty"`, "whole number of connections", "DB_MAX_OPEN_CONNS=50"}},
		{"DB_MAX_IDLE_CONNS", "a few", []string{`DB_MAX_IDLE_CONNS is "a few"`, "whole number of connections", "DB_MAX_IDLE_CONNS=50"}},
		{"DB_CONN_MAX_LIFETIME", "1h", []string{`DB_CONN_MAX_LIFETIME is "1h"`, "count of seconds", "DB_CONN_MAX_LIFETIME=900"}},
	} {
		t.Run(tc.key, func(t *testing.T) {
			env(t, "APP_KEY", testKey, tc.key, tc.value)

			_, err := bootstrap.LoadConfiguration()
			if err == nil {
				t.Fatalf("%s=%q was accepted; it parses as nothing, and the fallback is the zero that means the default pool", tc.key, tc.value)
			}
			for _, want := range tc.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the error does not say %q:\n%v", want, err)
				}
			}
		})
	}
}

// An explicit zero is refused rather than read as the default.
//
// It has two plausible meanings and only one is implemented: "give me the
// default" and "take the bound off". Reading it as the default answers the
// second person's question with the first person's answer and says nothing,
// which is the failure this reader exists to remove. Leaving the variable out is
// the way to ask for the default, and it is unambiguous.
func TestAnExplicitZeroOrNegativePoolSettingIsRefused(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"DB_MAX_OPEN_CONNS", "0"},
		{"DB_MAX_IDLE_CONNS", "0"},
		{"DB_CONN_MAX_LIFETIME", "0"},
		{"DB_MAX_OPEN_CONNS", "-1"},
		{"DB_CONN_MAX_LIFETIME", "-30"},
	} {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			env(t, "APP_KEY", testKey, tc.key, tc.value)

			_, err := bootstrap.LoadConfiguration()
			if err == nil {
				t.Fatalf("%s=%s was accepted; there is no unbounded pool to ask for, and unset is how the default is asked for", tc.key, tc.value)
			}
			if !strings.Contains(err.Error(), tc.key+" is "+strconv.Quote(tc.value)) {
				t.Errorf("the error does not name the variable and the value:\n%v", err)
			}
			if !strings.Contains(err.Error(), "greater than zero") {
				t.Errorf("the error does not say what is wrong with it:\n%v", err)
			}
		})
	}
}

// A variable a template rendered to nothing is not somebody asking for a
// number, so empty is absent and absent is the default.
//
// Refusing it would fail the boot of every deployment whose chart writes the key
// unconditionally, over a value nobody chose. Both readers this replaced already
// treated empty as absent, and so does config.String.
func TestAnEmptyPoolSettingIsTreatedAsUnset(t *testing.T) {
	env(t, "APP_KEY", testKey,
		"DB_MAX_OPEN_CONNS", "",
		"DB_MAX_IDLE_CONNS", "   ",
		"DB_CONN_MAX_LIFETIME", "")

	cfg, err := bootstrap.LoadConfiguration()
	if err != nil {
		t.Fatalf("an empty pool setting failed the boot: %v", err)
	}
	if cfg.Database.MaxOpenConns != 0 || cfg.Database.MaxIdleConns != 0 || cfg.Database.ConnMaxLifetime != 0 {
		t.Errorf("an empty value became a number: %d/%d/%v",
			cfg.Database.MaxOpenConns, cfg.Database.MaxIdleConns, cfg.Database.ConnMaxLifetime)
	}
}

// A bad value written in .env is refused too, which is where it will be written.
//
// The refusal reads the process environment, and .env reaches it through
// LoadDotenv earlier in the same function. Ordering those two the other way
// round would leave the check in place and stop it applying to the file almost
// every project actually keeps the setting in -- the refusal still there, still
// green, and reaching nothing.
func TestAPoolSettingFromTheDotenvFileIsRefusedAsWell(t *testing.T) {
	dir := t.TempDir()
	if err := writeFile(dir+"/.env", "DB_MAX_OPEN_CONNS=lots\n"); err != nil {
		t.Fatal(err)
	}
	chdir(t, dir)
	env(t, "APP_KEY", testKey)
	unset(t, "DB_MAX_OPEN_CONNS")

	_, err := bootstrap.LoadConfiguration()
	if err == nil {
		t.Fatal("a bad DB_MAX_OPEN_CONNS in .env was accepted; the refusal has to run after the file is loaded")
	}
	if !strings.Contains(err.Error(), `DB_MAX_OPEN_CONNS is "lots"`) {
		t.Errorf("the error does not name the variable and the value:\n%v", err)
	}
}

// The retired DB_* block stops the boot instead of being quietly ignored.
//
// The connection comes from one URL. A project whose .env still spells it out in
// parts connects to DATABASE_URL -- or, with none set, to the default SQLite
// file -- while six correct-looking values sit in the file configuring nothing.
// Every one of them is individually plausible, which is what makes the failure
// so slow to find: there is nothing wrong to see.
//
// The refusal was written for this and lived on a code path no application
// reaches. framework/config is a bridge no project imports; what every project
// calls is LoadConfiguration, and it parsed the URL without ever looking for the
// block it replaced.
func TestTheRetiredConnectionVariablesAreRefusedAtBoot(t *testing.T) {
	for _, key := range []string{
		"DB_CONNECTION", "DB_HOST", "DB_PORT", "DB_USERNAME", "DB_PASSWORD", "DB_DATABASE",
	} {
		t.Run(key, func(t *testing.T) {
			env(t, "APP_KEY", testKey, key, "pgsql")

			_, err := bootstrap.LoadConfiguration()
			if err == nil {
				t.Fatalf("%s was set and the boot went ahead; the application connects somewhere else and the file says otherwise", key)
			}
			for _, want := range []string{key + " is set", strconv.Quote("pgsql"), "DATABASE_URL=postgres://"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the error does not say %q:\n%v", want, err)
				}
			}
		})
	}
}

// And it is refused when it comes from .env, which is where it will be.
//
// The check reads the process environment, and .env reaches it through
// LoadDotenv earlier in the same function. Ordering those two the other way
// round leaves the refusal in place, green, and reaching the one file that
// carries the block in every project that still has it.
func TestARetiredConnectionVariableFromTheDotenvFileIsRefusedAsWell(t *testing.T) {
	dir := t.TempDir()
	if err := writeFile(dir+"/.env", "DB_CONNECTION=pgsql\nDB_HOST=127.0.0.1\n"); err != nil {
		t.Fatal(err)
	}
	chdir(t, dir)
	env(t, "APP_KEY", testKey)
	unset(t, "DB_CONNECTION", "DB_HOST")

	_, err := bootstrap.LoadConfiguration()
	if err == nil {
		t.Fatal("a retired DB_* block in .env was accepted; the refusal has to run after the file is loaded")
	}
	if !strings.Contains(err.Error(), "DB_CONNECTION is set") {
		t.Errorf("the error does not name the variable:\n%v", err)
	}
}

func writeFile(path, body string) error { return os.WriteFile(path, []byte(body), 0o600) }

// unset removes variables for the duration of one test, and puts back whatever
// the process had.
//
// t.Setenv registers the restore; unsetting afterwards is what makes the
// variable absent rather than empty, which is the state a deployment that never
// wrote it is actually in.
func unset(t *testing.T, keys ...string) {
	t.Helper()
	for _, key := range keys {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unsetting %s: %v", key, err)
		}
	}
}

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
