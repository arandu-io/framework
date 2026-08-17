package config

import (
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	hconfig "github.com/arandu-io/hesape/config"
)

// Env is the deployment environment. It gates everything that must never run
// outside development, starting with the debug error page.
//
// It is github.com/arandu-io/hesape/config.Env, and the alias is what keeps it
// one type rather than two: an Env read here is the same value a function
// written against that package takes, with the same Is and IsProduction
// methods, and the three spellings below are the same three constants.
type Env = hconfig.Env

// Supported environments.
const (
	EnvDev     = hconfig.EnvDev
	EnvStaging = hconfig.EnvStaging
	EnvProd    = hconfig.EnvProd
)

// appKeyLen is the required length of the application key, in bytes.
const appKeyLen = 32

// Config is a struct, not a map. Every field is validated at boot.
type Config struct {
	AppName  string
	Env      Env
	HTTPAddr string
	LogLevel slog.Level

	// AppKey signs session cookies and CSRF tokens. Exactly 32 bytes.
	AppKey []byte

	// Database is the connection, named the way a .env conventionally names it,
	// so nobody needs a translation table to read one.
	Database DatabaseConfig

	RedisURL string

	SessionTTL time.Duration
	CSRFTTL    time.Duration

	// TracingSecret enables the request Collector outside development for
	// requests carrying it in the X-Arandu-Trace header. Empty disables it,
	// which is the default: tracing must be opt-in, per deployment.
	TracingSecret string

	// Editor is the target of the "open in IDE" links on the error page.
	// One of vscode, cursor, goland, zed.
	Editor string
}

// Load reads the environment and validates it. It fails at boot, not on the
// first request.
//
// A .env in the working directory is read first, and it only fills variables
// the environment does not already define -- see loadEnvFile. It is read here
// rather than by the application, because a step the application has to
// remember is a step that gets forgotten: this one was, and `aru migrate`
// failed on every new project for it.
func Load() (Config, error) {
	if err := loadEnvFile(); err != nil {
		return Config{}, err
	}

	key, err := parseAppKey(env("APP_KEY", ""))
	if err != nil {
		return Config{}, err
	}
	database, err := loadDatabase()
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		AppName:       env("APP_NAME", "arandu-app"),
		Env:           Env(env("APP_ENV", string(EnvDev))),
		HTTPAddr:      env("HTTP_ADDR", ":8080"),
		AppKey:        key,
		Database:      database,
		RedisURL:      env("REDIS_URL", ""),
		SessionTTL:    duration("SESSION_TTL", 12*time.Hour),
		CSRFTTL:       duration("CSRF_TTL", 2*time.Hour),
		TracingSecret: env("ARANDU_TRACING_SECRET", ""),
		Editor:        env("ARANDU_EDITOR", "vscode"),
		LogLevel:      level(env("LOG_LEVEL", "info")),
	}
	return cfg, cfg.Validate()
}

// Validate reports the first configuration error, with the command that fixes
// it whenever one exists.
func (c Config) Validate() error {
	switch c.Env {
	case EnvDev, EnvStaging, EnvProd:
	default:
		return fmt.Errorf("invalid APP_ENV: %q (expected dev, staging or prod)", c.Env)
	}
	if len(c.AppKey) != appKeyLen {
		return fmt.Errorf("APP_KEY must be %d bytes, got %d (run `aru key:generate`)", appKeyLen, len(c.AppKey))
	}
	if err := c.Database.Validate(); err != nil {
		return err
	}
	if c.Env == EnvProd && c.LogLevel == slog.LevelDebug {
		return fmt.Errorf("LOG_LEVEL=debug is forbidden in production: it leaks request data into the log")
	}
	if c.Env != EnvDev && c.HTTPAddr == "" {
		return fmt.Errorf("HTTP_ADDR is required outside development")
	}
	return nil
}

// IsDev reports whether the debug surface is allowed to exist.
func (c Config) IsDev() bool { return c.Env == EnvDev }

// parseAppKey accepts both a raw 32-byte string and the "base64:" form emitted
// by `aru key:generate` -- a random 32-byte key is not printable, so the
// generated value is always encoded.
func parseAppKey(v string) ([]byte, error) {
	if v == "" {
		return nil, nil
	}
	if after, ok := strings.CutPrefix(v, "base64:"); ok {
		b, err := base64.StdEncoding.DecodeString(after)
		if err != nil {
			return nil, fmt.Errorf("APP_KEY is not valid base64: %w", err)
		}
		return b, nil
	}
	return []byte(v), nil
}

func env(k, def string) string {
	if v, ok := os.LookupEnv(k); ok && v != "" {
		return v
	}
	return def
}

// duration reads a value in seconds. Seconds, not a Go duration string,
// because this is read by deployment tooling as often as by people.
func duration(k string, def time.Duration) time.Duration {
	if v, ok := os.LookupEnv(k); ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return def
}

func level(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
