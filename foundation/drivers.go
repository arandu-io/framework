package foundation

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"

	"github.com/arandu-io/framework/foundation/bootstrap"
	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/http/middleware"
	"github.com/arandu-io/framework/mail"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/auth"
	"github.com/arandu-io/hesape/cache"
	"github.com/arandu-io/hesape/database"
	"github.com/arandu-io/hesape/filesystem"
	"github.com/arandu-io/hesape/queue"
	"github.com/arandu-io/hesape/session"
	"github.com/arandu-io/hesape/view"
)

// Drivers is everything the framework builds from the typed configuration: the
// database connection, the cache stores, the lock every replica shares, the
// session store and the CSRF issuer, the rate limiter, the queue, the mailer and
// the disks.
//
// An application changes a driver by changing its configuration, never by
// writing a factory: CACHE_STORE, SESSION_DRIVER, QUEUE_CONNECTION, MAIL_URL and
// FILESYSTEM_DISK name what is built, and a value that names something this
// binary cannot build stops NewDrivers naming the setting.
//
// The fields are filled once, by NewDrivers, and read afterwards. The value is
// what an application's providers build their services over, and what
// Configure composes the Application from:
//
//	drivers, err := foundation.NewDrivers(cfg.Settings)
//	app := providers.NewAppServiceProvider(cfg, drivers)
//	return foundation.Configure(drivers).WithProviders(app).Create()
//
// It is a struct of values rather than a container: nothing is resolved by
// name or by type, and a driver nobody reads is a field nobody reads.
type Drivers struct {
	// Settings is the configuration every driver below was built from.
	Settings bootstrap.Settings

	// DB is the application database, opened from DATABASE_URL with the pool
	// its settings state.
	DB *database.DB

	// Cache is every cache store this application has, by name: memory always,
	// and redis when REDIS_URL names an endpoint. CACHE_STORE names the default
	// one. A name the configuration does not define is an error naming it, and
	// Shared refuses a store that every replica cannot see.
	Cache *cache.CacheManager

	// Store is the store CACHE_STORE named: what the rate limit counts in and
	// where an idempotent write keeps its answer.
	Store *cache.Repository

	// SharedCache is the RESP store when a setting resolved it -- CACHE_STORE
	// or SESSION_DRIVER naming redis -- and nil when every store is kept inside
	// this process. It is what the health check reports and what the queue's
	// pause flag is written through.
	SharedCache cache.SharedStore

	// Locks issues locks every replica sees, over SharedCache, and is nil when
	// every store is kept inside this process. The isolated
	// commands and the scheduler's singleton claims take their locks here.
	//
	// Nil is a claim rather than an omission: a lock held inside one process is
	// invisible to the replica beside it, so outside development the isolated
	// commands refuse rather than report themselves isolated.
	Locks *cache.Locks

	// Locker is Locks in the shape the outbox relay takes, and nil when Locks
	// is. It is an untyped nil then, so a comparison with nil answers what it
	// asks.
	Locker Locker

	// Sessions is the only store allowed to create authenticated identity: the
	// session cookie, signed with the application key, over the backend
	// SESSION_DRIVER named, for SESSION_LIFETIME, with the Secure attribute the
	// CSRF and flash cookies carry.
	Sessions *security.SessionStore

	// CSRF issues and checks the token every form carries, bound to the
	// session, valid for CSRF_TTL, with the same Secure attribute.
	CSRF *security.CSRF

	// Limiter counts requests in Store, so the budget is one budget across the
	// replicas that share it rather than one per replica.
	Limiter *cache.RateLimiter

	// Idempotency is Store as an idempotent write's answer store. Every store
	// this builds can hold a lock; one that could not is refused by NewDrivers.
	Idempotency middleware.IdempotencyStore

	// Queue is the job store QUEUE_CONNECTION named: a table of the
	// application's own database, or the RESP queue.
	Queue queue.Queue

	// Mail is what sends, over the transport MAIL_URL named, from
	// MAIL_FROM_ADDRESS and MAIL_FROM_NAME.
	Mail *mail.Mailer

	// Disks is where files are kept: the disk FILESYSTEM_DISK named, which is
	// the default. A bucket disk -- r2 or s3 -- is built over the S3 protocol and
	// is also the cloud disk; the local disk writes under FILESYSTEM_ROOT.
	Disks *filesystem.Disks

	closeDB   func()
	closeOnce sync.Once
}

// The names the cache stores are known by. They are the values CACHE_STORE
// takes, so the word in the environment and the word in the wiring are one
// word; the RESP one is also the name its connector registers under.
const (
	memoryStore = string(bootstrap.CacheMemory)
	respStore   = string(bootstrap.CacheRedis)
)

// NewDrivers opens the database and builds every driver the settings describe.
//
// It dials nothing beyond the database adapter's own open: the RESP connectors
// connect on first use, so a cache that is down does not stop a process from
// starting, and the health check is what reports it. What it does at once is
// read the files the configuration names -- a certificate that is named and
// cannot be read stops it -- and refuse a setting that names a driver this
// binary does not link, with the two lines that add it.
//
// The caller owns the result and closes it with Close; an Application built by
// Configure closes it at Shutdown and at the end of every command.
func NewDrivers(s bootstrap.Settings) (*Drivers, error) {
	db, closeDB, err := database.Open(s.Database.Connection)
	if err != nil {
		return nil, err
	}
	d, err := newDrivers(s, db)
	if err != nil {
		closeDB()
		return nil, err
	}
	d.closeDB = closeDB
	return d, nil
}

// NewDriversOver builds every driver the settings describe over a database the
// caller already opened, and leaves closing it to the caller.
//
// It is NewDrivers for a test that shares one connection between several
// applications, or for a process that opened the database before the
// configuration was loaded.
func NewDriversOver(s bootstrap.Settings, db *database.DB) (*Drivers, error) {
	if db == nil {
		return nil, errors.New("foundation: NewDriversOver needs an open database")
	}
	return newDrivers(s, db)
}

func newDrivers(s bootstrap.Settings, db *database.DB) (*Drivers, error) {
	fw := s.Framework
	d := &Drivers{Settings: s, DB: db}

	if err := requireLinkedStores(s); err != nil {
		return nil, err
	}

	// The RESP store is defined only when REDIS_URL names an endpoint, so naming
	// it where there is none is an error at the boot rather than a process that
	// starts and shares nothing. Its certificates are read here, once.
	stores := map[string]cache.StoreConfig{
		memoryStore: {Driver: "array"},
	}
	var endpoint cache.Endpoint
	if s.Cache.Address != "" {
		var err error
		if endpoint, err = respEndpoint(s.Cache); err != nil {
			return nil, err
		}
		stores[respStore] = cache.StoreConfig{Driver: respStore, Endpoint: endpoint}
	}
	d.Cache = cache.NewCacheManager(cache.Config{
		Default: string(s.Cache.Store),
		Prefix:  s.Cache.Prefix,
		Stores:  stores,
	})

	var err error
	if d.Store, err = d.Cache.Store(string(s.Cache.Store)); err != nil {
		return nil, err
	}
	if s.Cache.Store == bootstrap.CacheRedis {
		if d.SharedCache, err = d.Cache.Shared(respStore); err != nil {
			return nil, err
		}
	}
	backend, err := d.sessionBackend()
	if err != nil {
		return nil, err
	}
	// The locks follow the shared store, whichever setting resolved it: a
	// server every replica reaches is where a lock every replica sees can be
	// held, whether the cache or the sessions brought it.
	if d.SharedCache != nil {
		d.Locks = cache.NewLocks(d.SharedCache)
		d.Locker = NewLocker(d.Locks)
	}
	d.Sessions = security.NewSessionStore(fw.App.Key, fw.Session.Lifetime, fw.Session.Secure, backend)
	d.CSRF = security.NewCSRF(fw.App.Key, s.Session.CSRFTTL).Secure(fw.Session.Secure)

	d.Limiter = cache.NewRateLimiter(d.Store.GetStore())
	idempotency, ok := d.Store.GetStore().(middleware.IdempotencyStore)
	if !ok {
		return nil, fmt.Errorf("foundation: the cache store %q cannot hold a lock, and an idempotent write keeps its keys in it", s.Cache.Store)
	}
	d.Idempotency = idempotency

	if d.Queue, err = openQueue(s, db, endpoint); err != nil {
		return nil, err
	}

	d.Mail = mail.New(mailTransport(s.Mail), view.NewRenderer(), mail.Address{
		Email: s.Mail.FromAddress,
		Name:  s.Mail.FromName,
	})
	if d.Disks, err = newDisks(s.Filesystems); err != nil {
		return nil, err
	}
	return d, nil
}

// Close closes the database NewDrivers opened. It is safe to call more than
// once and does nothing for drivers built over a database the caller opened.
func (d *Drivers) Close() error {
	if d == nil {
		return nil
	}
	d.closeOnce.Do(func() {
		if d.closeDB != nil {
			d.closeDB()
		}
	})
	return nil
}

// requireLinkedStores refuses the boot when CACHE_STORE or SESSION_DRIVER names
// the RESP store and no connector for it is linked into this binary.
//
// The error is the connector registry's, unchanged: it names the setting that
// asked, the `go get` and the blank import. The two settings are asked
// separately because either can name the store alone, and the error has to name
// the line that was actually written.
func requireLinkedStores(s bootstrap.Settings) error {
	if s.Cache.Store == bootstrap.CacheRedis {
		if err := cache.Linked("CACHE_STORE", respStore); err != nil {
			return err
		}
	}
	if s.Session.Driver == bootstrap.SessionRedis {
		if err := cache.Linked("SESSION_DRIVER", respStore); err != nil {
			return err
		}
	}
	return nil
}

// sessionBackend builds the backend SESSION_DRIVER named.
//
// The driver names a store, and not the cache's default one: sessions shared
// across replicas over a cache kept inside each process is a deployment, and a
// driver that names a store no other replica can see is refused here rather
// than signing half the visitors out on every request.
//
// The handler is the one the shared store keeps, so the session writes keys of
// its own and does not change the prefix the cache uses. The payload arrives
// still encoded, and session.Decode is where the subject type is named.
func (d *Drivers) sessionBackend() (security.SessionBackend, error) {
	driver := d.Settings.Session.Driver
	switch driver {
	case bootstrap.SessionMemory:
		return security.NewMemoryBackend(), nil

	case bootstrap.SessionRedis:
		shared, err := d.Cache.Shared(respStore)
		if err != nil {
			return nil, fmt.Errorf("SESSION_DRIVER %q names the cache store %q: %w", driver, respStore, err)
		}
		if d.SharedCache == nil {
			d.SharedCache = shared
		}
		keeper, ok := shared.(session.Keeper)
		if !ok {
			return nil, fmt.Errorf("SESSION_DRIVER %q names it, and the cache store %q is shared and cannot keep sessions", driver, respStore)
		}
		return security.NewSessionBackend(session.Decode[auth.Subject](keeper.Sessions())), nil

	default:
		// Unreachable through LoadSettings, which refuses the value first. A
		// configuration built in Go skips that check, and a driver nobody
		// recognises must not fall through to the in-process store.
		return nil, fmt.Errorf("SESSION_DRIVER has unsupported value %q; expected %s or %s",
			driver, bootstrap.SessionMemory, bootstrap.SessionRedis)
	}
}

// openQueue builds the queue QUEUE_CONNECTION named.
//
// The database one keeps its jobs in a table of the application's own database,
// which is what makes a job commitable by the same transaction as the row it is
// about. The RESP one is a connector of its own, opened over the endpoint the
// cache's RESP store is, because it is the same server described by the same
// settings -- and opening dials nothing.
func openQueue(s bootstrap.Settings, db *database.DB, endpoint cache.Endpoint) (queue.Queue, error) {
	switch s.Queue.Connection {
	case bootstrap.QueueDatabase:
		return queue.NewDatabaseQueue(db), nil

	case bootstrap.QueueRedis:
		driver := string(bootstrap.QueueRedis)
		if err := queue.Linked("QUEUE_CONNECTION", driver); err != nil {
			return nil, err
		}
		if s.Cache.Address == "" {
			return nil, fmt.Errorf("QUEUE_CONNECTION %q requires REDIS_URL", driver)
		}
		return queue.Open(driver, endpoint)

	default:
		return nil, fmt.Errorf("QUEUE_CONNECTION has unsupported value %q; expected %s or %s",
			s.Queue.Connection, bootstrap.QueueDatabase, bootstrap.QueueRedis)
	}
}

// mailTransport picks the transport MAIL_URL asked for. There are five, and a
// scheme that matches none is refused when the settings are read.
func mailTransport(cfg bootstrap.Mail) mail.Transport {
	switch cfg.Mailer {
	case bootstrap.MailerSMTP:
		return mail.SMTP{
			Host:       cfg.Host,
			Port:       strconv.Itoa(cfg.Port),
			Username:   cfg.Username,
			Password:   cfg.Password,
			Encryption: cfg.Encryption,
		}
	case bootstrap.MailerArray:
		return &mail.Array{}
	case bootstrap.MailerResend:
		return mail.Resend{Key: cfg.Key}
	case bootstrap.MailerSendGrid:
		return mail.SendGrid{Key: cfg.Key}
	default:
		return mail.Log{}
	}
}

// driversModule is the database and the drivers as a module: it reports the
// database on the readiness probe and closes what NewDrivers opened at
// shutdown. Configure registers it first, so it is closed last.
type driversModule struct{ drivers *Drivers }

var (
	_ Module   = driversModule{}
	_ Health   = driversModule{}
	_ Closable = driversModule{}
)

func (driversModule) Name() string                  { return "database" }
func (driversModule) Routes(*fhttp.Router)          {}
func (m driversModule) Close(context.Context) error { return m.drivers.Close() }

// Health pings the database, which every request that reads a row needs.
func (m driversModule) Health(ctx context.Context) error {
	return m.drivers.DB.PingContext(ctx)
}
