// Package bootstrap is the boot sequence: what runs once, in order, before the
// application answers anything.
//
// It composes the components; it is not one of them, which is why it lives here
// rather than with them.
//
// Two bootstrappers run, and they are the whole of it:
//
//	LoadConfiguration  reads the environment, .env included, and answers every
//	                   component's settings
//	HandleExceptions   builds the handler that answers when something fails
//
// Loading the environment is not a bootstrapper of its own. Nothing here is
// evaluated -- the reading is direct, in LoadConfiguration -- so a second one
// would be a second way to load one file.
//
// Registering and booting modules are not bootstrappers either. There is no
// container and no provider: what an application composes is modules,
// explicitly, and the two halves of that are Application.Register and
// Application.Boot. They are methods rather than bootstrappers because the list
// of modules is written by hand in bootstrap/app.go, where it can be read.
package bootstrap

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/arandu-io/hesape/cache"
	"github.com/arandu-io/hesape/config"
	"github.com/arandu-io/hesape/database"
	"github.com/arandu-io/hesape/filesystem"
	"github.com/arandu-io/hesape/log"
	"github.com/arandu-io/hesape/queue"
	"github.com/arandu-io/hesape/session"
	"github.com/arandu-io/hesape/view"
)

// Configuration is every component's settings, typed, built once at boot.
//
// It is not a config file and it is not a registry. Each component declares its
// own Config in its own package, because without a container nothing looks a
// value up by key -- the component is handed what it needs and the compiler
// checks the field. This struct is the one place that reads the environment and
// fills them in.
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
// touched rather than at start. One reader, one moment, one error.
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
	Queue      queue.Config
	View       view.Config

	// Observability is what the assembled application needs to explain itself.
	// It is here, beside the components, and not one of them -- see the type.
	Observability Observability

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

// Observability is how the assembled application reports on itself: what the
// root logger keeps, who may open the debug console, and where a stack frame
// links to.
//
// It is the one part of Configuration that is not a component's own Config, and
// the reason is what the three fields have in common: none of them configures a
// component. The channels, the handlers and the format belong to Log, and a
// channel carries its own level. These three belong to the application that was
// assembled -- they decide the cut of the root logger, whether the console
// answers at all outside development, and what an "open in IDE" link opens.
type Observability struct {
	// LogLevel is what the root logger keeps.
	//
	// It comes from LOG_LEVEL, spelled as one of the eight level names, and it
	// is the level of the logger the application is built with -- not of a
	// channel. A channel declares its own under Log.Channels; the root has no
	// channel to inherit one from, so the variable is read here as well, once,
	// and parsed into the type the logger takes.
	//
	// Debug is refused in production, where a request's arguments end up in the
	// log of a system holding customer data.
	LogLevel slog.Level

	// TracingSecret opens the debug console outside development, to a request
	// carrying it in the tracing header and to nothing else.
	//
	// Empty is the default and disables it. Tracing is opt-in per deployment: a
	// console that answers because nobody set a variable is a buffer of SQL,
	// bound arguments and dumps, across every tenant, reachable with no session.
	TracingSecret string

	// Editor is what the "open in IDE" links on the error page and the console
	// open. One of vscode, cursor, goland, zed.
	Editor string
}

// LoadConfiguration reads the environment once and answers every component's
// settings.
//
// It fails the process rather than returning a half-built Configuration. A
// framework that boots with a missing key and discovers it on the first request
// has moved a start-up error into production traffic.
//
// The variable names are the ones a .env already carries -- APP_NAME, APP_KEY,
// DB_CONNECTION and the rest keep the spelling and the meaning they have
// elsewhere, so a file moved across works unchanged. Where a default differs,
// the field says so.
//
// Where a default is not the obvious one, the field says why.
func LoadConfiguration() (Configuration, error) {
	// The file only fills what the environment has not already defined, and
	// never the other way round: a deploy sets a variable, and a stale .env in
	// the image must not win over it.
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

	// LOG_LEVEL is read once, here, because two things need it in two shapes:
	// the channels take the name and the root logger takes the parsed level.
	// Reading it twice is two answers to one variable the day one of the two
	// grows a fallback the other does not have.
	level := config.String("LOG_LEVEL", "info")

	observability, err := loadObservability(app, level)
	if err != nil {
		return Configuration{}, err
	}

	cfg := Configuration{
		App:           app,
		Session:       loadSession(app),
		Cache:         loadCache(),
		Database:      db,
		Log:           loadLog(app, level),
		Filesystem:    loadFilesystem(),
		Queue:         loadQueue(),
		View:          loadView(app),
		Observability: observability,
	}
	cfg.Repository = config.NewRepository(cfg.asMap())

	return cfg, nil
}

// minutes reads a variable written as a count of minutes.
//
// It exists for exactly one variable, SESSION_LIFETIME, and the reason is at
// its call site. Nothing else here is in minutes, and nothing else should be --
// config.Seconds is the form, because "3600" survives a Helm chart and "1h"
// does not.
func minutes(key string, fallback int) time.Duration {
	return time.Duration(config.Int(key, fallback)) * time.Minute
}

// loadSession answers the session settings.
func loadSession(app config.App) session.Config {
	// The cookie name is derived from the application name, and it is NOT
	// configurable on its own.
	//
	// The CSRF token is bound to the session, and a cookie name set
	// independently breaks that binding in a way nothing reports -- the token
	// stops matching and every form starts answering 419.
	cookie := strings.ToLower(strings.NewReplacer(" ", "_", ".", "_").Replace(app.Name)) + "_session"

	return session.Config{
		Driver: config.String("SESSION_DRIVER", "database"),
		Cookie: cookie,
		// SESSION_LIFETIME is read in MINUTES, and it is the one duration here
		// that is not seconds.
		//
		// The unit comes with the name: wherever SESSION_LIFETIME is already
		// written it means minutes, so reading it through config.Seconds like
		// every other duration in this file would turn an existing
		// SESSION_LIFETIME=120 into a two-minute session instead of a two-hour
		// one.
		//
		// That failure is silent and it is the worst shape available: everybody
		// stays signed in long enough for the change to look like it worked, and
		// then gets thrown out mid-form. A variable that means something else
		// under the same spelling is worse than a variable with a different name.
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

// loadCache answers the cache settings.
func loadCache() cache.Config {
	def := config.String("CACHE_STORE", "database")
	return cache.Config{
		Default: def,
		// The tenant is what separates one customer's keys from another's. The
		// prefix separates one deployment from another sharing a store.
		Prefix: config.String("CACHE_PREFIX", ""),
		Stores: map[string]cache.StoreConfig{
			"array":    {Driver: "array", Name: "array"},
			"file":     {Driver: "file", Name: "file", Path: config.String("CACHE_PATH", "storage/framework/cache/data")},
			"database": {Driver: "database", Name: "database"},
			"redis":    {Driver: "redis", Name: "redis"},
		},
	}
}

// loadObservability answers what the assembled application needs to explain
// itself.
//
// The level is parsed here rather than carried as a name, because that is the
// shape the root logger takes. An unknown name fails the process instead of
// falling back: a typo in LOG_LEVEL that quietly restores the default is how a
// deployment ends up logging more than it was told to, and the level a channel
// would have fallen back to is debug -- the one value production must not have.
func loadObservability(app config.App, level string) (Observability, error) {
	parsed, err := log.ParseLevel(level)
	if err != nil {
		return Observability{}, fmt.Errorf("LOG_LEVEL: %w", err)
	}
	if app.Env.IsProduction() && parsed == log.LevelDebug {
		return Observability{}, fmt.Errorf("LOG_LEVEL=debug is forbidden in production: it leaks request data into the log")
	}

	return Observability{
		LogLevel:      parsed,
		TracingSecret: config.String("ARANDU_TRACING_SECRET", ""),
		// vscode by default because it is what most people have. The link is
		// only ever built where the debug surface exists.
		Editor: config.String("ARANDU_EDITOR", "vscode"),
	}, nil
}

// loadLog answers the logging settings.
func loadLog(app config.App, level string) log.Config {
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

// loadFilesystem answers the filesystem settings.
func loadFilesystem() filesystem.Config {
	return filesystem.Config{
		Driver: config.String("FILESYSTEM_DISK", "local"),
		Root:   config.String("FILESYSTEM_ROOT", "storage/app"),
		URL:    config.String("FILESYSTEM_URL", ""),
		// Private, and never public by default.
		//
		// A file is customer data, and a disk that serves anything to anybody
		// who guesses a path is a leak with a directory name. Reaching a file
		// goes through the Grant; what is deliberately shared is shared by a
		// signed URL, which is why ServeSigned exists.
		Visibility:  config.String("FILESYSTEM_VISIBILITY", "private"),
		Disk:        config.String("FILESYSTEM_DISK", "local"),
		Prefix:      config.String("FILESYSTEM_PREFIX", ""),
		ServeSigned: config.Bool("FILESYSTEM_SERVE_SIGNED", true),
	}
}

// loadDatabase answers the database settings: where the database is, and how
// many connections to hold.
//
// The connection is one variable, not six, because six variables have
// thirty-two states of which one is right, and a URL either parses or says
// where it stopped.
//
// The pool is three more, and they are deliberately not part of that URL. How
// many connections to hold is a property of the process rather than of the
// database: two deployments of one application behind different traffic want
// different numbers against the same server, and putting them in the connection
// string would mean editing the address to change the size.
//
// Unset leaves all three at zero, and zero is the value that works. The
// database package reads a zero on any of them as the pool it keeps by default,
// never as database/sql's zero, which is an unbounded pool. So no number is
// written here: a default in this function as well would be a second place to
// change one, and the two would disagree the day only one was edited.
func loadDatabase() (database.Config, error) {
	raw := config.String("DATABASE_URL", database.DefaultURL)
	cfg, err := database.ParseURL(raw)
	if err != nil {
		return database.Config{}, fmt.Errorf("DATABASE_URL: %w", err)
	}

	cfg.MaxOpenConns = config.Int("DB_MAX_OPEN_CONNS", 0)
	cfg.MaxIdleConns = config.Int("DB_MAX_IDLE_CONNS", 0)
	// Seconds, like every duration read in this file except SESSION_LIFETIME:
	// "3600" survives a deployment template and "1h" does not.
	cfg.ConnMaxLifetime = config.Seconds("DB_CONN_MAX_LIFETIME", 0)

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
			// bcrypt is a compatibility default, and there are no previous
			// hashes to stay compatible with. argon2id is what
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

// loadQueue answers the queue settings.
func loadQueue() queue.Config {
	retry := config.Seconds("QUEUE_RETRY_AFTER", 90*time.Second)
	return queue.Config{
		Default: config.String("QUEUE_CONNECTION", "database"),
		Connections: map[string]queue.ConnectionConfig{
			"sync": {Driver: "sync", Name: "sync"},
			"database": {
				Driver:     "database",
				Name:       "database",
				Queue:      config.String("DB_QUEUE", "default"),
				Table:      config.String("DB_QUEUE_TABLE", "jobs"),
				RetryAfter: retry,
			},
			"redis": {
				Driver:     "redis",
				Name:       "redis",
				Queue:      config.String("REDIS_QUEUE", "default"),
				Connection: config.String("REDIS_QUEUE_CONNECTION", "default"),
				RetryAfter: retry,
			},
		},
		Failed: queue.FailedConfig{
			Driver: config.String("QUEUE_FAILED_DRIVER", "database"),
			Table:  config.String("QUEUE_FAILED_TABLE", "failed_jobs"),
		},
	}
}

// loadView answers the view settings.
//
// Two fields, because kyse compiles views into the binary: there is no path to
// search and no compiled cache to place. See view.Config.
func loadView(app config.App) view.Config {
	cfg := view.DefaultConfig()
	// The reload follows debug rather than a variable of its own. Serving the
	// script outside development costs a request per page for a feature nobody
	// outside development can use.
	cfg.Reload = app.Debug
	return cfg
}
