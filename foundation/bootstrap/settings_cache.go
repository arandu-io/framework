package bootstrap

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// CacheStore is which backend the cache runs on.
type CacheStore string

// The supported stores. Two, and they are not two ways to do one thing: memory
// is right for one process and wrong for two, and the interface is the same.
const (
	// CacheMemory keeps entries in the process. Right for development and for a
	// single replica; behind a load balancer, half the requests miss.
	CacheMemory CacheStore = "memory"
	// CacheRedis speaks RESP, which is Dragonfly, Redis, Valkey or KeyDB. Its
	// client is a connector the binary links by a blank import of
	// github.com/arandu-io/hesape/redis, so a binary that never names it does
	// not compile it; naming it without the import stops the boot saying so.
	CacheRedis CacheStore = "redis"
)

// defaultCachePort is the port RESP listens on when the URL names none.
const defaultCachePort = "6379"

// Cache is which backend the cache runs on, where the RESP server is, and how
// long an entry lives when the caller states no lifetime.
//
// The endpoint comes from one URL: the scheme is the transport, so a
// configuration cannot ask for one thing and carry the settings of another,
// and a credential cannot be left behind in a variable the new endpoint does
// not read.
//
//	REDIS_URL=redis://127.0.0.1:6379
//	REDIS_URL=redis://:password@cache.example.com:6379/1
//	REDIS_URL=rediss://:password@cache.example.com:6380    encrypted
type Cache struct {
	// Store is CACHE_STORE: memory, the default, or redis.
	Store CacheStore

	// Address is host:port, parsed out of REDIS_URL, and empty when it names no
	// endpoint.
	//
	// It is parsed whether or not Store is the RESP one, because the cache is
	// not the only thing that names that store: sessions shared across replicas
	// over a cache kept in each process is a deployment, and the queue speaks to
	// the same server. Every feature that needs the endpoint reads this field
	// rather than REDIS_URL.
	Address string

	// Password is the password half of the URL's userinfo:
	// redis://:password@host.
	Password string

	// Database is the numbered database the URL's path names.
	Database int

	// TLS says the connection is encrypted, and it is the scheme of the URL:
	// rediss:// asks for it and redis:// does not. On any network this process
	// does not own, it is the only correct setting.
	TLS bool

	// TLSCAFile is the authority that signed the server's certificate, when it
	// is not one the system already trusts: REDIS_CA_FILE.
	//
	// These four are file paths rather than certificates, because an
	// environment variable carries a string and a connection needs parsed keys.
	// They are read once, where the connection is built, and a file that is
	// named and cannot be read stops the boot.
	TLSCAFile string

	// TLSCertFile and TLSKeyFile are this client's certificate and its private
	// key, for a server that asks the client to prove who it is: REDIS_CERT_FILE
	// and REDIS_KEY_FILE. Naming one without the other is refused.
	TLSCertFile string
	TLSKeyFile  string

	// TLSServerName is the name the server's certificate must carry, when it is
	// not the host of the URL: REDIS_TLS_SERVER_NAME.
	TLSServerName string

	// Prefix is prepended to every key: CACHE_PREFIX, and the application name
	// followed by ":cache:" when it is not written. It separates deployments
	// sharing one server; the tenant is prepended per entry, from the Grant.
	Prefix string

	// TTL is how long an entry lives when the caller states no lifetime:
	// CACHE_TTL, in seconds, and ten minutes when it is not written.
	TTL time.Duration
}

func loadCacheSettings(base Configuration) (Cache, error) {
	store := CacheStore(env("CACHE_STORE", string(CacheMemory)))
	raw := env("REDIS_URL", "")
	switch store {
	case CacheMemory:
	case CacheRedis:
		if raw == "" {
			return Cache{}, fmt.Errorf("CACHE_STORE %q requires REDIS_URL", store)
		}
	default:
		return Cache{}, fmt.Errorf("CACHE_STORE has unsupported value %q; expected memory or redis", store)
	}

	ttl, err := envSeconds("CACHE_TTL", 10*time.Minute)
	if err != nil {
		return Cache{}, err
	}
	cfg := Cache{
		Store:         store,
		Prefix:        env("CACHE_PREFIX", base.App.Name+":cache:"),
		TTL:           ttl,
		TLSCAFile:     env("REDIS_CA_FILE", ""),
		TLSCertFile:   env("REDIS_CERT_FILE", ""),
		TLSKeyFile:    env("REDIS_KEY_FILE", ""),
		TLSServerName: env("REDIS_TLS_SERVER_NAME", ""),
	}
	// Parsed whenever it is set, and not only when the cache uses it: a URL that
	// is set and cannot be read is a configuration error wherever it was meant
	// to be used.
	if raw != "" {
		endpoint, err := parseRedisURL(raw)
		if err != nil {
			return Cache{}, err
		}
		cfg.Address = endpoint.Address
		cfg.Password = endpoint.Password
		cfg.Database = endpoint.Database
		cfg.TLS = endpoint.TLS
	}
	return cfg, nil
}

// redisEndpoint is what one URL says about where the RESP server is.
type redisEndpoint struct {
	Address  string
	Password string
	Database int
	TLS      bool
}

// parseRedisURL reads REDIS_URL into the endpoint the client dials.
//
// A bad value is returned to the boot rather than falling back to the
// in-process store: falling back would let every replica cache into memory of
// its own while reporting a successful boot.
func parseRedisURL(raw string) (redisEndpoint, error) {
	raw = strings.TrimSpace(raw)

	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return redisEndpoint{}, fmt.Errorf("REDIS_URL has invalid URL value %q", raw)
	}

	// rediss is the encrypted connection and redis the one in the clear, the
	// way smtps and smtp name the two SMTP sessions.
	encrypted := strings.EqualFold(u.Scheme, "rediss")
	if !encrypted && !strings.EqualFold(u.Scheme, "redis") {
		return redisEndpoint{}, fmt.Errorf("REDIS_URL uses unsupported scheme %q; this application supports redis and rediss", u.Scheme)
	}

	host := u.Hostname()
	if host == "" {
		return redisEndpoint{}, fmt.Errorf("REDIS_URL names no host")
	}
	port := u.Port()
	if port == "" {
		port = defaultCachePort
	}

	out := redisEndpoint{Address: net.JoinHostPort(host, port), TLS: encrypted}

	if u.User != nil {
		out.Password, _ = u.User.Password()
		// Only the password is carried. The client speaks to the implicit user,
		// and a URL naming another would authenticate as somebody the
		// connection cannot become.
		if name := u.User.Username(); name != "" && name != "default" {
			return redisEndpoint{}, fmt.Errorf("REDIS_URL names unsupported user %q; expected the default user", name)
		}
	}

	if name := strings.TrimPrefix(u.Path, "/"); name != "" {
		number, err := strconv.Atoi(name)
		if err != nil || number < 0 {
			return redisEndpoint{}, fmt.Errorf("REDIS_URL has invalid database path %q; expected a non-negative integer", u.Path)
		}
		out.Database = number
	}

	return out, nil
}
