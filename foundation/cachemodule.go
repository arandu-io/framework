// The one thing in the framework that registers a key-value connection as a
// module, and the interface that lets it do so without importing the driver.
//
// The shape is the one NewLocker uses: the composition vocabulary is declared
// here, and what lives beside it is the few lines that turn a value from a
// component into that shape.

package foundation

import (
	"context"

	fhttp "github.com/arandu-io/framework/http"
)

// CacheConnection is the part of a key-value connection a module needs.
//
// An interface declared here rather than the connection type itself, and the
// reason is a budget: the package that holds that type pulls a RESP driver and
// nine modules behind it, and the core is capped at golang.org/x/crypto. Go
// satisfies an interface by shape, so a connection fits this without either
// side naming the other.
//
// The two methods are what a module does with a connection and all it does:
// answer whether the server is reachable, and give the pool back.
type CacheConnection interface {
	// Ping reports whether the server answers.
	Ping(ctx context.Context) error

	// Close releases the pool.
	Close() error
}

// NewCacheModule returns a module that health-checks a key-value connection and
// releases its pool on shutdown.
//
//	k.Register(foundation.NewCacheModule("cache", conn))
//
// Registering it is what puts "the key-value store is down" on the health
// endpoint beside the database, rather than leaving it to surface as a class of
// request failures somebody correlates by hand. It is also what returns the
// pool when the process stops: a connection nothing owns is a connection
// nothing closes.
//
// The name is the module identifier and is used as given -- it appears in the
// health report and in `aru doctor`, so it should say which store this is when
// an application holds more than one.
func NewCacheModule(name string, conn CacheConnection) *CacheModule {
	return &CacheModule{name: name, conn: conn}
}

// CacheModule registers a key-value connection with the application.
//
// It carries no HTTP surface: what it contributes is a health answer and a
// close, which are the two things the composition asks of a module that owns a
// connection.
type CacheModule struct {
	name string
	conn CacheConnection
}

var (
	_ Module   = (*CacheModule)(nil)
	_ Health   = (*CacheModule)(nil)
	_ Closable = (*CacheModule)(nil)
)

// Name is the module identifier, as given to NewCacheModule.
func (m *CacheModule) Name() string { return m.name }

// Routes registers nothing: a connection has no HTTP surface of its own.
func (*CacheModule) Routes(*fhttp.Router) {}

// Health reports whether the store answers.
func (m *CacheModule) Health(ctx context.Context) error { return m.conn.Ping(ctx) }

// Close releases the pool on shutdown.
func (m *CacheModule) Close(context.Context) error { return m.conn.Close() }
