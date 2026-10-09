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

// Unset, empty and blank are two hours, as every other setting here keeps its
// default for a variable a template rendered to nothing.
func TestSessionLifetimeDefaultsToTwoHours(t *testing.T) {
	for _, value := range []string{"unset", "", "   "} {
		env(t, "APP_KEY", testKey)
		if value == "unset" {
			unset(t, "SESSION_LIFETIME")
		} else {
			t.Setenv("SESSION_LIFETIME", value)
		}

		cfg, err := bootstrap.LoadConfiguration()
		if err != nil {
			t.Fatalf("SESSION_LIFETIME %q: LoadConfiguration: %v", value, err)
		}
		if got, want := cfg.Session.Lifetime, 2*time.Hour; got != want {
			t.Errorf("SESSION_LIFETIME %q became %v, want the default %v", value, got, want)
		}
	}
}

// A lifetime that is not a whole number of minutes greater than zero stops the
// boot. The reader this replaced fell back on a word it could not parse and
// kept a zero, which is a session that expires as it is written; and a number
// too large for a duration wraps around to a negative one, which is the same.
func TestASessionLifetimeThatIsNotWholeMinutesStopsTheBoot(t *testing.T) {
	for _, value := range []string{"0", "-5", "two-hours", "1.5", "90m", "153722867280912931"} {
		t.Run(value, func(t *testing.T) {
			env(t, "APP_KEY", testKey, "SESSION_LIFETIME", value)

			_, err := bootstrap.LoadConfiguration()
			if err == nil {
				t.Fatalf("SESSION_LIFETIME=%q was accepted", value)
			}
			for _, want := range []string{
				"SESSION_LIFETIME is " + strconv.Quote(value),
				"whole number of minutes greater than zero",
				"SESSION_LIFETIME=120",
				"Leave it\nunset to keep the default",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the error does not say %q:\n%v", want, err)
				}
			}
		})
	}
}

// The largest lifetime a duration holds is still read, so the bound refuses
// only what would wrap around.
func TestTheLongestSessionLifetimeIsRead(t *testing.T) {
	longest := int64(1<<63-1) / int64(time.Minute)
	env(t, "APP_KEY", testKey, "SESSION_LIFETIME", strconv.FormatInt(longest, 10))

	cfg, err := bootstrap.LoadConfiguration()
	if err != nil {
		t.Fatalf("LoadConfiguration: %v", err)
	}
	if got, want := cfg.Session.Lifetime, time.Duration(longest)*time.Minute; got != want {
		t.Errorf("Lifetime = %v, want %v", got, want)
	}
}

// Every session variable the record store does not read is refused when it
// asks for a cookie or a store the record store does not write, and the
// message names it and says what the cookie is.
//
// Each of these used to be read into a struct nothing built a store from:
// SESSION_ENCRYPT=true booted and encrypted nothing, SESSION_DOMAIN booted and
// wrote a host-only cookie, and SESSION_TTL was a second lifetime beside
// SESSION_LIFETIME.
func TestASessionSettingNothingReadsStopsTheBoot(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"SESSION_ENCRYPT", "true"},
		{"SESSION_ENCRYPT", "yes"},
		{"SESSION_EXPIRE_ON_CLOSE", "1"},
		{"SESSION_EXPIRE_ON_CLOSE", "ON"},
		{"SESSION_FILES", "storage/framework/sessions"},
		{"SESSION_TABLE", "sessions"},
		{"SESSION_CONNECTION", "pgsql"},
		{"SESSION_STORE", "redis"},
		{"SESSION_PATH", "/app"},
		{"SESSION_PATH", "//"},
		{"SESSION_DOMAIN", "example.com"},
		{"SESSION_DOMAIN", ".example.com"},
		{"SESSION_SAME_SITE", "strict"},
		{"SESSION_SAME_SITE", "none"},
		{"SESSION_TTL", "43200"},
		{"SESSION_TTL", "12h"},
	} {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			env(t, "APP_KEY", testKey, tc.key, tc.value)

			_, err := bootstrap.LoadConfiguration()
			if err == nil {
				t.Fatalf("%s=%q was accepted, and nothing reads it", tc.key, tc.value)
			}
			// The message is wrapped for a terminal, so it is compared with
			// the line breaks folded into spaces.
			said := strings.Join(strings.Fields(err.Error()), " ")
			for _, want := range []string{
				tc.key + " is " + strconv.Quote(tc.value),
				"nothing reads it",
				"written by the record store",
				"on path /",
				"for the host that answered and no other",
				"SameSite=Lax",
				"signed and not encrypted",
				"expires with SESSION_LIFETIME",
			} {
				if !strings.Contains(said, want) {
					t.Errorf("the error does not say %q:\n%v", want, err)
				}
			}
		})
	}
}

// SESSION_TTL was seconds, and the refusal says what to write instead, in
// minutes, so the session lasts what it lasted.
func TestSessionTTLIsRefusedInFavourOfSessionLifetimeInMinutes(t *testing.T) {
	for value, want := range map[string]string{
		"43200":  "SESSION_LIFETIME=720",
		"90":     "SESSION_LIFETIME=2",
		"12h":    "SESSION_LIFETIME=120",
		"-1":     "SESSION_LIFETIME=120",
		" 3600 ": "SESSION_LIFETIME=60",
	} {
		env(t, "APP_KEY", testKey, "SESSION_TTL", value)

		_, err := bootstrap.LoadConfiguration()
		if err == nil {
			t.Fatalf("SESSION_TTL=%q was accepted", value)
		}
		for _, w := range []string{want, "in minutes", "was a count of\nseconds"} {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("SESSION_TTL=%q: the error does not say %q:\n%v", value, w, err)
			}
		}
	}
}

// The value that asks for what the record store already writes is kept, and so
// is a variable left blank: an older .env carries SESSION_PATH=/ and
// SESSION_DOMAIN= with nothing after it, and neither asks for anything.
func TestASessionSettingThatAsksForWhatTheStoreWritesIsKept(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"SESSION_ENCRYPT", "false"},
		{"SESSION_ENCRYPT", "0"},
		{"SESSION_EXPIRE_ON_CLOSE", "off"},
		{"SESSION_EXPIRE_ON_CLOSE", "No"},
		{"SESSION_PATH", "/"},
		{"SESSION_PATH", " / "},
		{"SESSION_SAME_SITE", "lax"},
		{"SESSION_SAME_SITE", "Lax"},
		{"SESSION_SAME_SITE", "LAX"},
		{"SESSION_DOMAIN", ""},
		{"SESSION_DOMAIN", "  "},
		{"SESSION_FILES", ""},
		{"SESSION_TABLE", ""},
		{"SESSION_CONNECTION", " "},
		{"SESSION_STORE", ""},
		{"SESSION_TTL", ""},
		{"SESSION_TTL", "   "},
	} {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			env(t, "APP_KEY", testKey, tc.key, tc.value)

			if _, err := bootstrap.LoadConfiguration(); err != nil {
				t.Errorf("%s=%q was refused, and it asks for what the store already writes: %v", tc.key, tc.value, err)
			}
		})
	}
}

// SESSION_DRIVER is the application's: it names the handler the application
// builds its store with. The loader neither reads it nor refuses it, whatever
// it says, and the Repository does not publish a driver or a lifetime for
// anything to read instead of the struct.
func TestSessionDriverIsNeitherReadNorRefused(t *testing.T) {
	for _, value := range []string{"memory", "redis", "kv", "database", "anything"} {
		env(t, "APP_KEY", testKey, "SESSION_DRIVER", value)

		cfg, err := bootstrap.LoadConfiguration()
		if err != nil {
			t.Fatalf("SESSION_DRIVER=%s: LoadConfiguration: %v", value, err)
		}
		for _, key := range []string{"session.driver", "session.lifetime", "session"} {
			if cfg.Repository.Has(key) {
				t.Errorf("the Repository publishes %s, and nothing reads it", key)
			}
		}
	}
}

// And a refused session variable is refused from .env as well, which is where
// it will be written.
func TestASessionSettingFromTheDotenvFileIsRefusedAsWell(t *testing.T) {
	dir := t.TempDir()
	if err := writeFile(dir+"/.env", "SESSION_DOMAIN=example.com\n"); err != nil {
		t.Fatal(err)
	}
	chdir(t, dir)
	env(t, "APP_KEY", testKey)
	unset(t, "SESSION_DOMAIN")

	_, err := bootstrap.LoadConfiguration()
	if err == nil {
		t.Fatal("SESSION_DOMAIN=example.com in .env was accepted; the refusal has to run after the file is loaded")
	}
	if !strings.Contains(err.Error(), `SESSION_DOMAIN is "example.com"`) {
		t.Errorf("the error does not name the variable and the value:\n%v", err)
	}
}

// A cookie that travels in the clear because nobody set a variable is the
// failure that looks like nothing at all, so the default closes: Secure in
// every environment but dev, whatever APP_URL says, because behind a proxy that
// ends TLS neither the URL nor the scheme this process sees is the browser's.
// SESSION_SECURE_COOKIE, when it is set, decides in every environment.
func TestTheSessionCookieIsSecureUnlessTheEnvironmentIsDev(t *testing.T) {
	for _, tc := range []struct {
		env, url, variable string
		want               bool
	}{
		{"prod", "http://localhost:8080", "", true},
		{"prod", "https://loja.example", "", true},
		{"staging", "http://loja.internal", "", true},
		{"dev", "http://localhost:8080", "", false},
		{"dev", "https://loja.test", "", false},
		{"prod", "https://loja.example", "false", false},
		{"staging", "http://loja.internal", "false", false},
		{"dev", "http://localhost:8080", "true", true},
		// An APP_ENV nobody wrote is parsed as dev, so it answers as dev does.
		{"", "http://localhost:8080", "", false},
	} {
		env(t, "APP_KEY", testKey, "APP_ENV", tc.env, "APP_URL", tc.url, "SESSION_SECURE_COOKIE", tc.variable)

		cfg, err := bootstrap.LoadConfiguration()
		if err != nil {
			t.Fatalf("APP_ENV=%s: LoadConfiguration: %v", tc.env, err)
		}
		if cfg.Session.Secure != tc.want {
			t.Errorf("APP_ENV=%s APP_URL=%s SESSION_SECURE_COOKIE=%q: Secure = %v, want %v",
				tc.env, tc.url, tc.variable, cfg.Session.Secure, tc.want)
		}
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

// TestAnEditorTheLinkTableDoesNotKnowStopsTheBoot covers the typo whose only
// other symptom is an error page where nothing is clickable.
//
// A name outside the table draws every stack frame without its link, and it
// does that silently -- on the page somebody opened because something else was
// already wrong. Boot is the moment to say so.
func TestAnEditorTheLinkTableDoesNotKnowStopsTheBoot(t *testing.T) {
	env(t, "APP_KEY", testKey)
	t.Setenv("ARANDU_EDITOR", "vim")

	_, err := bootstrap.LoadConfiguration()
	if err == nil {
		t.Fatal("ARANDU_EDITOR=vim was accepted: every frame on the error page would be drawn without a link")
	}
	if !strings.Contains(err.Error(), "ARANDU_EDITOR") {
		t.Errorf("the message does not name the variable to fix: %v", err)
	}
}

// TestAnEmptyEditorIsAllowed: no editor is a configuration, not a mistake.
// EditorLink answers "" for it, and the frames are drawn without links, which
// is what somebody who set nothing asked for.
func TestAnEmptyEditorIsAllowed(t *testing.T) {
	if err := (bootstrap.Observability{}).Validate(); err != nil {
		t.Fatalf("an unset editor and an unset tracing secret were refused: %v", err)
	}
}

// TestATracingSecretTooShortToKeepStopsTheBoot covers the value that switches
// the console on without protecting it.
//
// The secret is the whole gate on the debug console outside development, and it
// is compared against a header on a route that answers 404 to everything else:
// no session, no throttle, unlimited attempts. Its length is the only cost of
// guessing it.
func TestATracingSecretTooShortToKeepStopsTheBoot(t *testing.T) {
	env(t, "APP_KEY", testKey)
	t.Setenv("ARANDU_TRACING_SECRET", "x")

	_, err := bootstrap.LoadConfiguration()
	if err == nil {
		t.Fatal("a one-character tracing secret was accepted: it opens the console to anybody who tries a few headers")
	}
	if !strings.Contains(err.Error(), "ARANDU_TRACING_SECRET") {
		t.Errorf("the message does not name the variable to fix: %v", err)
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

// Every boolean the loader reads, with the value it has when nothing is set.
//
// APP_DEBUG is read by config.Load and not by a field here, so it is checked
// through what it decides; the others are fields of the Configuration.
// SESSION_ENCRYPT and SESSION_EXPIRE_ON_CLOSE have no field: nothing reads
// them, so false is kept and true is refused, and read is nil for both.
var loaderBooleans = []struct {
	key  string
	read func(bootstrap.Configuration) bool
}{
	{"APP_DEBUG", func(c bootstrap.Configuration) bool { return c.App.Debug }},
	{"SESSION_EXPIRE_ON_CLOSE", nil},
	{"SESSION_ENCRYPT", nil},
	{"SESSION_SECURE_COOKIE", func(c bootstrap.Configuration) bool { return c.Session.Secure }},
	{"FILESYSTEM_SERVE_SIGNED", func(c bootstrap.Configuration) bool { return c.Filesystem.ServeSigned }},
}

// A boolean that is written and cannot be read stops the boot.
//
// The reader underneath falls back on a word it does not know, so
// SESSION_SECURE_COOKIE=sometimes was false in dev and true everywhere else,
// and nothing anywhere said the value had been dropped. A value padded with a
// space is one the reader does not know either, and the quoted value in the
// message shows it. The two booleans nothing reads are held to the same rule:
// a word that is neither true nor false is refused as unreadable before it is
// refused as unread.
//
// The message is asserted and not merely the error, because an error that does
// not name the variable sends somebody through six files looking for it.
func TestABooleanThatCannotBeReadStopsTheBoot(t *testing.T) {
	for _, b := range loaderBooleans {
		for _, value := range []string{"sometimes", "yes-please", "t", "2", " true"} {
			t.Run(b.key+"="+value, func(t *testing.T) {
				env(t, "APP_KEY", testKey, "APP_ENV", "staging", b.key, value)

				_, err := bootstrap.LoadConfiguration()
				if err == nil {
					t.Fatalf("%s=%q was accepted; it reads as neither true nor false, and the default would have been used in silence", b.key, value)
				}
				for _, want := range []string{
					b.key + " is " + strconv.Quote(value),
					"read as a boolean",
					"true, false, 1, 0, yes, no, on and off",
					"Leave it unset to keep the default",
				} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("the error does not say %q:\n%v", want, err)
					}
				}
			})
		}
	}
}

// Every spelling the message offers is read, in any case, and decides the
// value rather than the default.
//
// Each one is written against the default it would otherwise get, so a
// spelling the loader dropped to the default would read wrong here. For the two
// booleans nothing reads, every false spelling boots and every true one is
// refused as unread.
func TestEverySpellingTheRefusalOffersIsRead(t *testing.T) {
	for _, b := range loaderBooleans {
		for value, want := range map[string]bool{
			"true": true, "TRUE": true, "1": true, "yes": true, "On": true,
			"false": false, "False": false, "0": false, "no": false, "OFF": false,
		} {
			t.Run(b.key+"="+value, func(t *testing.T) {
				// Production refuses debug, so APP_DEBUG is read in staging,
				// where both of its answers boot.
				env(t, "APP_KEY", testKey, "APP_ENV", "staging", b.key, value)

				cfg, err := bootstrap.LoadConfiguration()
				if b.read == nil {
					switch {
					case want && err == nil:
						t.Errorf("%s=%q was accepted, and nothing reads it", b.key, value)
					case want && !strings.Contains(err.Error(), "nothing reads it"):
						t.Errorf("%s=%q was refused for the wrong reason: %v", b.key, value, err)
					case !want && err != nil:
						t.Errorf("%s=%q was refused, and it asks for what the store already writes: %v", b.key, value, err)
					}
					return
				}
				if err != nil {
					t.Fatalf("%s=%q: LoadConfiguration: %v", b.key, value, err)
				}
				if got := b.read(cfg); got != want {
					t.Errorf("%s=%q read as %v, want %v", b.key, value, got, want)
				}
			})
		}
	}
}

// Unset, empty and blank are the default, as they are for every other setting
// here: a template that rendered a key to nothing is not somebody choosing.
func TestAnUnsetOrBlankBooleanKeepsItsDefault(t *testing.T) {
	defaults := map[string]bool{
		"APP_DEBUG":               false,
		"SESSION_SECURE_COOKIE":   true,
		"FILESYSTEM_SERVE_SIGNED": true,
	}
	for _, value := range []string{"", "   ", "unset"} {
		for _, b := range loaderBooleans {
			env(t, "APP_KEY", testKey, "APP_ENV", "staging")
			if value == "unset" {
				unset(t, b.key)
			} else {
				t.Setenv(b.key, value)
			}

			cfg, err := bootstrap.LoadConfiguration()
			if err != nil {
				t.Fatalf("%s %q: LoadConfiguration: %v", b.key, value, err)
			}
			if b.read == nil {
				continue
			}
			if got := b.read(cfg); got != defaults[b.key] {
				t.Errorf("%s %q read as %v, want the default %v", b.key, value, got, defaults[b.key])
			}
		}
	}
}

// And it is refused when it comes from .env, which is where it will be written.
func TestABooleanFromTheDotenvFileIsRefusedAsWell(t *testing.T) {
	dir := t.TempDir()
	if err := writeFile(dir+"/.env", "SESSION_ENCRYPT=yes-please\n"); err != nil {
		t.Fatal(err)
	}
	chdir(t, dir)
	env(t, "APP_KEY", testKey)
	unset(t, "SESSION_ENCRYPT")

	_, err := bootstrap.LoadConfiguration()
	if err == nil {
		t.Fatal("SESSION_ENCRYPT=yes-please in .env was accepted; the refusal has to run after the file is loaded")
	}
	if !strings.Contains(err.Error(), `SESSION_ENCRYPT is "yes-please"`) {
		t.Errorf("the error does not name the variable and the value:\n%v", err)
	}
}
