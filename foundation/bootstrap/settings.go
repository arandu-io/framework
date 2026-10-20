package bootstrap

import (
	"fmt"
	"log/slog"

	"github.com/arandu-io/hesape/config"
	"github.com/arandu-io/hesape/database"
)

// Settings is the whole configuration an application's drivers are built from:
// what the kernel parsed for itself, and one section per kind of driver --
// the cache stores, the database, the disks, the request boundary, the log, the
// mail transport, the queue and the session.
//
// The sections are typed, and none of them is read by key. An application that
// needs a setting of its own embeds Settings in a struct of its own and adds
// the section beside it:
//
//	type Config struct {
//		bootstrap.Settings
//		Services Services
//	}
//
// so cfg.Cache.Store and cfg.Services.Stripe are both fields, and the
// application never copies a section the framework already reads.
//
// It is filled once, by LoadSettings, and every refusal happens there: a
// variable that is present and cannot be used stops the boot naming itself,
// rather than falling back to a default the deployment did not choose.
type Settings struct {
	// Framework is what foundation.New takes: one struct per component, filled
	// from the environment once. The sections below are read from the same
	// environment and never re-read what it already holds -- the key, the
	// environment, the connection and the log level are taken from here.
	Framework Configuration

	App         App
	Cache       Cache
	Database    Database
	Filesystems Filesystems
	HTTP        HTTP
	Logging     Logging
	Mail        Mail
	Queue       Queue
	Session     SessionSettings
}

// App is the identity of the application, as the sections and the drivers
// read it: the name, the environment, the canonical address rendered as a
// string, the listening address, the zone and the locale.
//
// It is a view of Configuration.App, never a second reading of the
// environment: two readers of APP_URL are two answers the day one of them
// grows a default the other does not have.
type App struct {
	// Name appears in the page title, in the log and in outgoing mail.
	Name string
	// Env gates everything that must never run outside development.
	Env config.Env
	// URL is the canonical address, used to build absolute links from a job or
	// a scheduled task, where there is no request to read the host from.
	URL string
	// HTTPAddr is what the server listens on.
	HTTPAddr string
	// Timezone is the zone dates are rendered in. Storage is always UTC.
	Timezone string
	// Locale is the default language tag.
	Locale string
}

// IsDev reports whether the debug surface is allowed to exist.
func (a App) IsDev() bool { return a.Env.Is(config.EnvDev) }

// Database is the connection the application opens, and the pool it keeps.
//
// Nothing here is read from the environment: DATABASE_URL and the three pool
// settings are parsed into Configuration.Database, and this is that value under
// the name the drivers read.
type Database struct {
	// Connection is the engine, its credentials and its pool. Hand it to the
	// adapter; never build a DSN by hand.
	Connection database.Config
}

// Logging is what is written, and how much of it.
type Logging struct {
	// Level is the lowest severity written. It is Configuration's own
	// Observability.LogLevel, which refuses debug in production.
	Level slog.Level

	// Format is json or text: LOG_FORMAT, which defaults to text in
	// development and to json everywhere else.
	Format string

	// Sampling drops this fraction of info-level records under load, expressed
	// as one in N: LOG_SAMPLING. Zero keeps all of them.
	Sampling int
}

// LoadSettings reads the environment once and answers every section, or the
// first error.
//
// It is LoadConfiguration followed by SettingsFrom, so the .env is read once
// and the variables the kernel already parsed are not read again.
func LoadSettings() (Settings, error) {
	base, err := LoadConfiguration()
	if err != nil {
		return Settings{}, err
	}
	return SettingsFrom(base)
}

// SettingsFrom builds the sections on top of a Configuration the kernel already
// parsed, reading the variables only the sections own.
//
// It validates the base before anything is built from it, and answers the
// first invalid setting rather than a partial Settings. It is separate from
// LoadSettings so a test can hand it a Configuration built in Go.
func SettingsFrom(base Configuration) (Settings, error) {
	if err := base.App.Validate(); err != nil {
		return Settings{}, fmt.Errorf("framework application configuration: %w", err)
	}
	// LoadConfiguration never answers a lifetime that is not positive, so this
	// is a base built in Go. A store built with it would expire every session
	// as it was written, and the first sign would be a sign-in that signs
	// nobody in.
	if base.Session.Lifetime <= 0 {
		return Settings{}, fmt.Errorf("framework session configuration: Lifetime is %s, and a session has to last longer than that; "+
			"SESSION_LIFETIME states it in minutes when the configuration is loaded, and a configuration built in Go states Session.Lifetime", base.Session.Lifetime)
	}

	cache, err := loadCacheSettings(base)
	if err != nil {
		return Settings{}, err
	}
	filesystems, err := loadFilesystemSettings()
	if err != nil {
		return Settings{}, err
	}
	httpSettings, err := loadHTTPSettings()
	if err != nil {
		return Settings{}, err
	}
	logging, err := loadLoggingSettings(base)
	if err != nil {
		return Settings{}, err
	}
	mail, err := loadMailSettings(base)
	if err != nil {
		return Settings{}, err
	}
	queue, err := loadQueueSettings()
	if err != nil {
		return Settings{}, err
	}
	// After the cache, because the session names one of its stores.
	session, err := loadSessionSettings(cache)
	if err != nil {
		return Settings{}, err
	}

	return Settings{
		Framework:   base,
		App:         appSettings(base),
		Cache:       cache,
		Database:    Database{Connection: base.Database},
		Filesystems: filesystems,
		HTTP:        httpSettings,
		Logging:     logging,
		Mail:        mail,
		Queue:       queue,
		Session:     session,
	}, nil
}

// appSettings renders what was already parsed. A nil address is what a
// configuration built by hand leaves behind; on a real boot App.Validate
// refuses it first.
func appSettings(base Configuration) App {
	address := ""
	if base.App.URL != nil {
		address = base.App.URL.String()
	}
	return App{
		Name:     base.App.Name,
		Env:      base.App.Env,
		URL:      address,
		HTTPAddr: base.App.HTTPAddr,
		// A nil location renders as UTC.
		Timezone: base.App.Timezone.String(),
		Locale:   base.App.Locale,
	}
}

// loadLoggingSettings reads the format and the sampling. The level is the one
// the kernel parsed, so LOG_LEVEL keeps one reader.
func loadLoggingSettings(base Configuration) (Logging, error) {
	fallback := "json"
	if base.App.Env.Is(config.EnvDev) {
		fallback = "text"
	}
	format := env("LOG_FORMAT", fallback)
	switch format {
	case "json", "text":
	default:
		return Logging{}, fmt.Errorf("LOG_FORMAT has unsupported value %q; expected json or text", format)
	}
	sampling, err := envInt("LOG_SAMPLING", 0)
	if err != nil {
		return Logging{}, err
	}
	return Logging{
		Level:    base.Observability.LogLevel,
		Format:   format,
		Sampling: sampling,
	}, nil
}
