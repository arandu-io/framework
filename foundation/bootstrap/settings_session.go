package bootstrap

import (
	"fmt"
	"time"
)

// SessionDriver is the cache store session state is kept in.
//
// It names a store rather than inheriting the cache's, so a deployment can
// share its sessions while caching inside each process: what the cache loses to
// a restart is work, and what the sessions lose is everybody who was signed in.
type SessionDriver string

// The supported drivers.
const (
	// SessionMemory keeps sessions in the process. Right for one instance and
	// wrong for two: behind a load balancer, half the requests land on the
	// replica that never saw the login.
	SessionMemory SessionDriver = "memory"
	// SessionRedis keeps sessions over RESP, shared by every replica. It names
	// the RESP store independently of CACHE_STORE, and that store is a
	// connector the binary links by a blank import.
	SessionRedis SessionDriver = "redis"
)

// SessionSettings is where session state is kept, and how long a CSRF token
// lasts.
//
// It is named apart from Session, which is what the cookie is written with --
// whether it is Secure and how long it lasts -- because the two are read by two
// different things: the cookie by the record store, and this by whatever
// builds the store's backend and the CSRF issuer.
type SessionSettings struct {
	// Driver is SESSION_DRIVER: memory, the default, or redis.
	Driver SessionDriver

	// CSRFTTL is how long a CSRF token stays valid: CSRF_TTL, in seconds, and
	// two hours when it is not written. It is its own setting rather than the
	// session's lifetime, because a token that outlives the page it was
	// rendered on is a token that can be replayed.
	CSRFTTL time.Duration
}

// loadSessionSettings reads the driver against the cache stores that are
// already defined, so a driver naming a store the cache configuration did not
// define is refused at the boot rather than at the first request.
func loadSessionSettings(cache Cache) (SessionSettings, error) {
	driver := SessionDriver(env("SESSION_DRIVER", string(SessionMemory)))
	switch driver {
	case SessionMemory:
	case SessionRedis:
		if cache.Address == "" {
			return SessionSettings{}, fmt.Errorf("SESSION_DRIVER %q requires REDIS_URL", driver)
		}
	default:
		return SessionSettings{}, fmt.Errorf("SESSION_DRIVER has unsupported value %q; expected memory or redis", driver)
	}
	// SESSION_SECURE is retired and refused rather than ignored:
	// SESSION_SECURE=false written for a deployment served over http would
	// otherwise be dropped in silence, and the first sign would be every session
	// disappearing between two requests.
	if env("SESSION_SECURE", "") != "" {
		return SessionSettings{}, fmt.Errorf("SESSION_SECURE is retired; remove it. " +
			"SESSION_SECURE_COOKIE decides the Secure attribute: set it to false only for a staging deployment served over http")
	}
	csrfTTL, err := envSeconds("CSRF_TTL", 2*time.Hour)
	if err != nil {
		return SessionSettings{}, err
	}
	return SessionSettings{Driver: driver, CSRFTTL: csrfTTL}, nil
}
