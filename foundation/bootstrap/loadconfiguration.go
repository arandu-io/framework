// Package bootstrap is the boot sequence: what runs once, in order, before the
// application answers anything.
//
// It is Illuminate\Foundation\Bootstrap, and it is here rather than in the
// collection for the reason ADR 0049 gives: of the 37 illuminate/* packages
// laravel/framework publishes, none is Foundation. The boot composes the
// components; it is not one of them.
//
// Laravel runs six bootstrappers. Five have an equivalent here --
// LoadEnvironmentVariables, LoadConfiguration, HandleExceptions,
// RegisterProviders and BootProviders. The sixth, RegisterFacades, has none:
// ADR 0002 rejected facades, and there is no alias loader to register them
// into.
package bootstrap

import (
	"fmt"
	"strings"
	"time"

	"github.com/arandu-io/hesape/cache"
	"github.com/arandu-io/hesape/config"
	"github.com/arandu-io/hesape/database"
	"github.com/arandu-io/hesape/filesystem"
	"github.com/arandu-io/hesape/log"
	"github.com/arandu-io/hesape/session"
)

// Configuration is every component's settings, typed, built once at boot.
//
// It is not a config file and it is not a registry. ADR 0051 settled the shape:
// each component declares its own Config in its own package, because without a
// container nothing looks a value up by key -- the component is handed what it
// needs and the compiler checks the field. This struct is the one place that
// reads the environment and fills them in.
//
// The difference from a Repository of dotted keys is where a mistake surfaces.
// A wrong field here does not compile. A wrong key in a map compiles, returns
// the zero value, and shows up on the first request that happened to need it.
//
// # Why the components are not asked to load themselves
//
// Every field below could have been a Load() in its own package, and that is
// one Load per component reading the same environment at different moments.
// Boot order would stop being visible, a variable read twice could answer twice,
// and the failure of any of them would arrive whenever that component was first
// touched rather than at start. One reader, one moment, one error (RULE 9).
type Configuration struct {
	// App is the application itself: name, environment, key, URL, locale.
	// It is the only one with a loader of its own, because hesape/config.Load
	// already validates the key and refuses debug in production.
	App config.App

	Session    session.Config
	Cache      cache.Config
	Database   database.Config
	Log        log.Config
	Filesystem filesystem.Config

	// Repository answers the components that read configuration through an
	// interface rather than a struct -- hashing.Config is one, and it is three
	// keys and one method by design, so that hesape/hashing does not import a
	// configuration package to read them.
	//
	// It is a reader over the same settings, never a second store. Nothing the
	// framework depends on is read through it, and a key set here and nowhere
	// else configures nothing.
	Repository *config.Repository
}

// LoadConfiguration is Illuminate\Foundation\Bootstrap\LoadConfiguration: it
// reads the environment once and answers every component's settings.
//
// It fails the process rather than returning a half-built Configuration. A
// framework that boots with a missing key and discovers it on the first request
// has moved a start-up error into production traffic.
//
// The defaults are Laravel's, from laravel/framework/config/*.php, and the
// environment variable names are Laravel's too -- somebody moving a .env across
// should find the same names doing the same thing. Where a default differs, the
// field says so.
func LoadConfiguration() (Configuration, error) {
	// The file only fills what the environment has not already defined, and
	// never the other way round. hesape/config/dotenv.go documents the
	// precedence and calls inverting it the thing never to do: a deploy sets a
	// variable, and a stale .env in the image must not win over it.
	if err := config.LoadDotenv(); err != nil {
		return Configuration{}, fmt.Errorf("loading .env: %w", err)
	}

	app, err := config.Load()
	if err != nil {
		return Configuration{}, fmt.Errorf("loading the application configuration: %w", err)
	}

	db, err := loadDatabase()
	if err != nil {
		return Configuration{}, err
	}

	cfg := Configuration{
		App:        app,
		Session:    loadSession(app),
		Cache:      loadCache(),
		Database:   db,
		Log:        loadLog(app),
		Filesystem: loadFilesystem(),
	}
	cfg.Repository = config.NewRepository(cfg.asMap())

	return cfg, nil
}

// minutes reads a variable written as a count of minutes.
//
// It exists for exactly one variable, SESSION_LIFETIME, and the reason is at
// its call site: Laravel spells it that way, and a name carried over has to
// carry its unit with it. Nothing else here is in minutes, and nothing else
// should be -- config.Seconds is the form, because "3600" survives a Helm chart
// and "1h" does not.
func minutes(key string, fallback int) time.Duration {
	return time.Duration(config.Int(key, fallback)) * time.Minute
}

// loadSession answers config/session.php.
func loadSession(app config.App) session.Config {
	// The cookie name is derived from the application name, as Laravel derives
	// it from APP_NAME, and it is NOT configurable on its own.
	//
	// hesape/session documents why: the CSRF token is bound to the session, and
	// a cookie name set independently breaks that binding in a way nothing
	// reports -- the token stops matching and every form starts answering 419.
	cookie := strings.ToLower(strings.NewReplacer(" ", "_", ".", "_").Replace(app.Name)) + "_session"

	return session.Config{
		Driver: config.String("SESSION_DRIVER", "database"),
		Cookie: cookie,
		// SESSION_LIFETIME is read in MINUTES, and it is the one duration here
		// that is not seconds.
		//
		// Laravel's config/session.php reads it as minutes and defaults to 120.
		// Every other duration in this file goes through config.Seconds, and the
		// consistent thing would be to read this one the same way -- which would
		// turn a .env carried over from Laravel, saying SESSION_LIFETIME=120,
		// into a two-minute session instead of a two-hour one.
		//
		// That failure is silent and it is the worst shape available: everybody
		// stays signed in long enough for the change to look like it worked, and
		// then gets thrown out mid-form. Keeping Laravel's name obliges keeping
		// Laravel's unit; a variable that means something else under the same
		// spelling is worse than a variable with a different name.
		Lifetime:      minutes("SESSION_LIFETIME", 120),
		ExpireOnClose: config.Bool("SESSION_EXPIRE_ON_CLOSE", false),
		Encrypt:       config.Bool("SESSION_ENCRYPT", false),
		Files:         config.String("SESSION_FILES", "storage/framework/sessions"),
		Connection:    config.String("SESSION_CONNECTION", ""),
		Table:         config.String("SESSION_TABLE", "sessions"),
		Store:         config.String("SESSION_STORE", ""),
		Path:          config.String("SESSION_PATH", "/"),
		Domain:        config.String("SESSION_DOMAIN", ""),
		// Secure defaults to whether the application URL is https, and not to
		// false. A cookie that travels in the clear because nobody set a
		// variable is the failure that looks like nothing at all.
		Secure: config.Bool("SESSION_SECURE_COOKIE", app.URL != nil && app.URL.Scheme == "https"),
	}
}

// loadCache answers config/cache.php.
func loadCache() cache.Config {
	def := config.String("CACHE_STORE", "database")
	return cache.Config{
		Default: def,
		// Laravel derives the prefix from the application name. Here it is the
		// tenant that separates one customer's keys from another's (RULE 14),
		// and the prefix separates one deployment from another sharing a store.
		Prefix: config.String("CACHE_PREFIX", ""),
		Stores: map[string]cache.StoreConfig{
			"array":    {Driver: "array", Name: "array"},
			"file":     {Driver: "file", Name: "file", Path: config.String("CACHE_PATH", "storage/framework/cache/data")},
			"database": {Driver: "database", Name: "database"},
			"redis":    {Driver: "redis", Name: "redis"},
		},
	}
}

// loadLog answers config/logging.php.
func loadLog(app config.App) log.Config {
	level := config.String("LOG_LEVEL", "info")
	return log.Config{
		Default: config.String("LOG_CHANNEL", "stack"),
		Env:     string(app.Env),
		Channels: map[string]log.ChannelConfig{
			"stack":  {Driver: "stack", Name: "stack", Channels: []string{config.String("LOG_STACK", "single")}},
			"single": {Driver: "single", Name: "single", Level: level, Path: config.String("LOG_PATH", "storage/logs/arandu.log")},
			"daily":  {Driver: "daily", Name: "daily", Level: level, Path: config.String("LOG_PATH", "storage/logs/arandu.log"), Days: config.Int("LOG_DAILY_DAYS", 14)},
			"stderr": {Driver: "stderr", Name: "stderr", Level: level},
		},
	}
}

// loadFilesystem answers config/filesystems.php.
func loadFilesystem() filesystem.Config {
	return filesystem.Config{
		Driver: config.String("FILESYSTEM_DISK", "local"),
		Root:   config.String("FILESYSTEM_ROOT", "storage/app"),
		URL:    config.String("FILESYSTEM_URL", ""),
		// Private, and not Laravel's public.
		//
		// A file is customer data, and a disk that serves anything to anybody
		// who guesses a path is a leak with a directory name (RULE 14). Reaching
		// a file goes through the Grant; what is deliberately shared is shared
		// by a signed URL, which is why ServeSigned exists.
		Visibility:  config.String("FILESYSTEM_VISIBILITY", "private"),
		Disk:        config.String("FILESYSTEM_DISK", "local"),
		Prefix:      config.String("FILESYSTEM_PREFIX", ""),
		ServeSigned: config.Bool("FILESYSTEM_SERVE_SIGNED", true),
	}
}

// loadDatabase answers config/database.php.
//
// One variable, not six. ADR 0035 decided the connection is a URL, because six
// variables have thirty-two states of which one is right, and a URL either
// parses or says where it stopped.
func loadDatabase() (database.Config, error) {
	raw := config.String("DATABASE_URL", database.DefaultURL)
	cfg, err := database.ParseURL(raw)
	if err != nil {
		return database.Config{}, fmt.Errorf("DATABASE_URL: %w", err)
	}
	return cfg, nil
}

// asMap flattens the typed settings into the dotted keys a Repository answers.
//
// It exists for the components that read configuration through an interface --
// hashing is the one today -- and for an application that genuinely needs a key
// whose name it only knows at run time.
//
// It is built FROM the structs and never the other way round. That direction is
// the whole point: the struct is the source, the map is a view of it, and a key
// that appears here without a field behind it configures nothing.
func (c Configuration) asMap() map[string]any {
	return map[string]any{
		"app": map[string]any{
			"name":   c.App.Name,
			"env":    string(c.App.Env),
			"debug":  c.App.Debug,
			"locale": c.App.Locale,
		},
		"hashing": map[string]any{
			// argon2id, and not bcrypt.
			//
			// Laravel defaults to bcrypt for compatibility with hashes written
			// by every previous version of it. There are no previous Arandu
			// hashes to be compatible with, and argon2id is what
			// golang.org/x/crypto carries -- the one third-party dependency the
			// core takes, and it takes it for this.
			"driver": config.String("HASH_DRIVER", "argon2id"),
			"argon": map[string]any{
				"memory":  config.Int("ARGON_MEMORY", 65536),
				"time":    config.Int("ARGON_TIME", 4),
				"threads": config.Int("ARGON_THREADS", 1),
			},
		},
		"session": map[string]any{
			"driver":   c.Session.Driver,
			"lifetime": c.Session.Lifetime,
		},
		"cache": map[string]any{"default": c.Cache.Default, "prefix": c.Cache.Prefix},
		"log":   map[string]any{"default": c.Log.Default},
	}
}
