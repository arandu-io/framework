package config_test

import (
	"encoding/base64"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/framework/config"
	"github.com/arandu-io/framework/data"
)

func validConfig() config.Config {
	return config.Config{
		AppName:  "test",
		Env:      config.EnvDev,
		HTTPAddr: ":8080",
		AppKey:   make([]byte, config.AppKeyLen),
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
	raw := make([]byte, config.AppKeyLen)
	for i := range raw {
		raw[i] = byte(i)
	}
	t.Setenv("APP_KEY", "base64:"+base64.StdEncoding.EncodeToString(raw))
	t.Setenv("DB_CONNECTION", "sqlite")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(cfg.AppKey) != config.AppKeyLen {
		t.Fatalf("key length = %d, want %d", len(cfg.AppKey), config.AppKeyLen)
	}
	if string(cfg.AppKey) != string(raw) {
		t.Fatal("the decoded key does not match what was encoded")
	}
}

func TestLoadRejectsBrokenBase64Key(t *testing.T) {
	t.Setenv("APP_KEY", "base64:not!valid!base64")
	t.Setenv("DB_CONNECTION", "sqlite")

	if _, err := config.Load(); err == nil {
		t.Fatal("a malformed base64 APP_KEY was accepted")
	}
}

func TestLoadAppliesDefaults(t *testing.T) {
	t.Setenv("APP_KEY", strings.Repeat("k", config.AppKeyLen))
	t.Setenv("DB_CONNECTION", "sqlite")

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
	if cfg.Editor != "vscode" {
		t.Errorf("default Editor = %q, want vscode", cfg.Editor)
	}
	if cfg.TracingSecret != "" {
		t.Error("tracing must be off by default: it costs memory on every request")
	}
}

func TestLoadReadsTTLInSeconds(t *testing.T) {
	t.Setenv("APP_KEY", strings.Repeat("k", config.AppKeyLen))
	t.Setenv("DB_CONNECTION", "sqlite")
	t.Setenv("SESSION_TTL", "60")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.SessionTTL != time.Minute {
		t.Fatalf("SessionTTL = %v, want 1m", cfg.SessionTTL)
	}
}
