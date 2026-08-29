package unit

import (
	"encoding/base64"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/framework/config"
	"github.com/arandu-io/framework/data"
)

// What the package requires, written out here rather than read from it.
//
// The two used to be exported constants, and a test that asserts a length
// against the constant the code checks with asserts nothing: change the
// constant and the test moves with it. These are the numbers the requirement
// is, so a change to either has to be made twice, on purpose.
const (
	appKeyLen         = 32
	defaultSQLitePath = "database/database.sqlite"
)

func validConfig() config.Config {
	return config.Config{
		AppName:  "test",
		Env:      config.EnvDev,
		HTTPAddr: ":8080",
		AppKey:   make([]byte, appKeyLen),
		Database: config.DatabaseConfig{
			Connection: data.DialectSQLite,
			Database:   "database/database.sqlite",
		},
		SessionTTL: time.Hour,
		CSRFTTL:    time.Hour,
	}
}

func TestValidateAcceptsAValidConfig(t *testing.T) {
	if err := validConfig().Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidateRejectsUnknownEnv(t *testing.T) {
	cfg := validConfig()
	cfg.Env = "production" // a common and expensive typo: it is "prod"

	err := cfg.Validate()
	if err == nil {
		t.Fatal("an unknown APP_ENV was accepted, which would silently enable the debug page")
	}
	if !strings.Contains(err.Error(), "dev, staging or prod") {
		t.Errorf("the message must list the valid values, got: %v", err)
	}
}

// TestValidateRejectsWrongKeyLength also checks that the message names the
// command that fixes it -- an error that only states the problem costs a search.
func TestValidateRejectsWrongKeyLength(t *testing.T) {
	cfg := validConfig()
	cfg.AppKey = []byte("too-short")

	err := cfg.Validate()
	if err == nil {
		t.Fatal("a short APP_KEY was accepted")
	}
	if !strings.Contains(err.Error(), "aru key:generate") {
		t.Errorf("the message must name the fix, got: %v", err)
	}
}

func TestValidateRequiresADatabase(t *testing.T) {
	cfg := validConfig()
	cfg.Database.Database = ""

	if err := cfg.Validate(); err == nil {
		t.Fatal("an empty DB_DATABASE was accepted: the failure would surface on the first query instead of at boot")
	}
}

// TestDebugLogIsForbiddenInProduction: debug level records request payloads, so
// leaving it on in production is a data leak into the log aggregator.
func TestDebugLogIsForbiddenInProduction(t *testing.T) {
	cfg := validConfig()
	cfg.Env = config.EnvProd
	cfg.LogLevel = slog.LevelDebug

	if err := cfg.Validate(); err == nil {
		t.Fatal("LOG_LEVEL=debug was accepted in production")
	}
}

// TestValidateRejectsANonPositiveTTL covers the two durations.
//
// Zero is what a variable that failed to parse leaves behind, and both zeros
// are invisible until traffic arrives: a session that expires as it is written
// signs everybody out on their next request, and a token that expires as it is
// issued answers 419 on every form.
func TestValidateRejectsANonPositiveTTL(t *testing.T) {
	for name, broken := range map[string]func(*config.Config){
		"SESSION_TTL": func(c *config.Config) { c.SessionTTL = 0 },
		"CSRF_TTL":    func(c *config.Config) { c.CSRFTTL = -time.Second },
	} {
		cfg := validConfig()
		broken(&cfg)

		err := cfg.Validate()
		if err == nil {
			t.Errorf("%s was accepted at zero or below", name)
			continue
		}
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the message does not name %s: %v", name, err)
		}
	}
}

// TestValidateRejectsARedisURLThatCannotBeDialled covers the address that
// parses as a URL and is not one of these.
func TestValidateRejectsARedisURLThatCannotBeDialled(t *testing.T) {
	for _, raw := range []string{
		"http://127.0.0.1:6379",  // the wrong protocol entirely
		"redis://",               // a scheme and nothing to connect to
		"127.0.0.1:6379",         // the host without a scheme, which parses as one
		"redis://%zz@host:6379/", // not parseable at all
	} {
		cfg := validConfig()
		cfg.RedisURL = raw

		if err := cfg.Validate(); err == nil {
			t.Errorf("REDIS_URL=%q was accepted; the failure would arrive on the first cache read", raw)
		}
	}
}

// TestValidateNeverReturnsRedisCredentials keeps every validation branch from
// turning a boot-time configuration error into a credential disclosure.
func TestValidateNeverReturnsRedisCredentials(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		secret string
		defect string
	}{
		{
			name:   "malformed URL",
			raw:    "redis://:parse-secret@cache.example/%zz",
			secret: "parse-secret",
			defect: "URL",
		},
		{
			name:   "unsupported scheme",
			raw:    "http://:scheme-secret@cache.example:6379",
			secret: "scheme-secret",
			defect: "redis:// or rediss://",
		},
		{
			name:   "missing host",
			raw:    "redis://:host-secret@",
			secret: "host-secret",
			defect: "host",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			cfg.RedisURL = tt.raw

			err := cfg.Validate()
			if err == nil {
				t.Fatal("a broken REDIS_URL was accepted")
			}
			message := err.Error()
			if !strings.Contains(message, "REDIS_URL") {
				t.Errorf("the message does not name REDIS_URL: %v", err)
			}
			if !strings.Contains(message, tt.defect) {
				t.Errorf("the message does not identify the defect %q: %v", tt.defect, err)
			}
			if strings.Contains(message, tt.raw) {
				t.Errorf("the message reproduces the credential-bearing URL: %v", err)
			}
			if strings.Contains(message, tt.secret) {
				t.Errorf("the message exposes the Redis password: %v", err)
			}
		})
	}
}

// TestValidateAcceptsRedisURLsThatWork, because a check that refuses everything
// refuses the working configuration too.
func TestValidateAcceptsRedisURLsThatWork(t *testing.T) {
	for _, raw := range []string{
		"redis://127.0.0.1:6379",
		"redis://:password@cache.example.com:6379/1",
		"rediss://:password@cache.example.com:6380",
		"", // no Redis at all: the other stores are selected by leaving it out
	} {
		cfg := validConfig()
		cfg.RedisURL = raw

		if err := cfg.Validate(); err != nil {
			t.Errorf("REDIS_URL=%q was refused: %v", raw, err)
		}
	}
}

// TestValidateRejectsAnEditorTheLinkTableDoesNotKnow: an unknown name draws
// every stack frame on the error page without its link, and draws it that way
// silently.
func TestValidateRejectsAnEditorTheLinkTableDoesNotKnow(t *testing.T) {
	cfg := validConfig()
	cfg.Editor = "vim"

	err := cfg.Validate()
	if err == nil {
		t.Fatal("ARANDU_EDITOR=vim was accepted")
	}
	if !strings.Contains(err.Error(), "ARANDU_EDITOR") {
		t.Errorf("the message does not name the variable to fix: %v", err)
	}
}

// TestValidateRejectsATracingSecretTooShortToKeep: the secret is the whole gate
// on the debug console outside development, and its length is the only cost of
// guessing it.
func TestValidateRejectsATracingSecretTooShortToKeep(t *testing.T) {
	cfg := validConfig()
	cfg.TracingSecret = "x"

	err := cfg.Validate()
	if err == nil {
		t.Fatal("a one-character tracing secret was accepted")
	}
	if !strings.Contains(err.Error(), "ARANDU_TRACING_SECRET") {
		t.Errorf("the message does not name the variable to fix: %v", err)
	}
}

// TestValidateAcceptsAnEmptyTracingSecretAndEditor: empty is the default of
// both, and it means the console is off and the frames carry no links. Refusing
// it would refuse every application that never asked for either.
func TestValidateAcceptsAnEmptyTracingSecretAndEditor(t *testing.T) {
	cfg := validConfig()
	cfg.TracingSecret = ""
	cfg.Editor = ""

	if err := cfg.Validate(); err != nil {
		t.Fatalf("an unconfigured console and editor were refused: %v", err)
	}
}

func TestIsDev(t *testing.T) {
	for env, want := range map[config.Env]bool{
		config.EnvDev:     true,
		config.EnvStaging: false,
		config.EnvProd:    false,
	} {
		cfg := validConfig()
		cfg.Env = env
		if got := cfg.IsDev(); got != want {
			t.Errorf("IsDev for %q = %v, want %v", env, got, want)
		}
	}
}

// TestLoadDecodesBase64Key covers the format `aru key:generate` emits: 32 random
// bytes are not printable, so the value in .env is always encoded.
func TestLoadDecodesBase64Key(t *testing.T) {
	raw := make([]byte, appKeyLen)
	for i := range raw {
		raw[i] = byte(i)
	}
	t.Setenv("APP_KEY", "base64:"+base64.StdEncoding.EncodeToString(raw))
	t.Setenv("DATABASE_URL", "sqlite://database/database.sqlite")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(cfg.AppKey) != appKeyLen {
		t.Fatalf("key length = %d, want %d", len(cfg.AppKey), appKeyLen)
	}
	if string(cfg.AppKey) != string(raw) {
		t.Fatal("the decoded key does not match what was encoded")
	}
}

func TestLoadRejectsBrokenBase64Key(t *testing.T) {
	t.Setenv("APP_KEY", "base64:not!valid!base64")
	t.Setenv("DATABASE_URL", "sqlite://database/database.sqlite")

	if _, err := config.Load(); err == nil {
		t.Fatal("a malformed base64 APP_KEY was accepted")
	}
}

func TestLoadAppliesDefaults(t *testing.T) {
	t.Setenv("APP_KEY", strings.Repeat("k", appKeyLen))
	t.Setenv("DATABASE_URL", "sqlite://database/database.sqlite")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Env != config.EnvDev {
		t.Errorf("default Env = %q, want dev", cfg.Env)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("default HTTPAddr = %q, want :8080", cfg.HTTPAddr)
	}
	if cfg.SessionTTL != 12*time.Hour {
		t.Errorf("default SessionTTL = %v, want 12h", cfg.SessionTTL)
	}
	if cfg.CSRFTTL != 2*time.Hour {
		t.Errorf("default CSRFTTL = %v, want 2h", cfg.CSRFTTL)
	}
	if cfg.Editor != "vscode" {
		t.Errorf("default Editor = %q, want vscode", cfg.Editor)
	}
	if cfg.TracingSecret != "" {
		t.Error("tracing must be off by default: it costs memory on every request")
	}
}

func TestLoadReadsTTLsInSeconds(t *testing.T) {
	t.Setenv("APP_KEY", strings.Repeat("k", appKeyLen))
	t.Setenv("DATABASE_URL", "sqlite://database/database.sqlite")
	t.Setenv("SESSION_TTL", "60")
	t.Setenv("CSRF_TTL", "90")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.SessionTTL != time.Minute {
		t.Fatalf("SessionTTL = %v, want 1m", cfg.SessionTTL)
	}
	if cfg.CSRFTTL != 90*time.Second {
		t.Fatalf("CSRFTTL = %v, want 1m30s", cfg.CSRFTTL)
	}
}

func TestLoadRejectsInvalidTTLEnvironmentValues(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "malformed session TTL", key: "SESSION_TTL", value: "one hour"},
		{name: "zero session TTL", key: "SESSION_TTL", value: "0"},
		{name: "negative session TTL", key: "SESSION_TTL", value: "-1"},
		{name: "malformed CSRF TTL", key: "CSRF_TTL", value: "one hour"},
		{name: "zero CSRF TTL", key: "CSRF_TTL", value: "0"},
		{name: "negative CSRF TTL", key: "CSRF_TTL", value: "-1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("APP_KEY", strings.Repeat("k", appKeyLen))
			t.Setenv("DATABASE_URL", "sqlite://database/database.sqlite")
			t.Setenv("SESSION_TTL", "60")
			t.Setenv("CSRF_TTL", "60")
			t.Setenv(tt.key, tt.value)

			_, err := config.Load()
			if err == nil {
				t.Fatalf("%s=%q was replaced by a default", tt.key, tt.value)
			}
			if !strings.Contains(err.Error(), tt.key) {
				t.Errorf("the message does not name %s: %v", tt.key, err)
			}
		})
	}
}
