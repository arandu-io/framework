// Sessions, answered by github.com/arandu-io/hesape/session, and -- for the
// address a guard remembers before sending somebody to the sign-in screen --
// by github.com/arandu-io/hesape/http.
//
// Every name here is an alias or a forward: the constants, the errors and the
// option of hesape/session, and SessionStore, SessionBackend and MemoryBackend
// of hesape/http.

package security

import (
	"time"

	hhttp "github.com/arandu-io/hesape/http"
	"github.com/arandu-io/hesape/session"
)

// SessionCookieName is the cookie the framework reads and writes. It is fixed
// on purpose: a configurable cookie name buys nothing and breaks the CSRF
// binding when two parts of a project disagree about it.
//
// It is session.CookieName under a name that repeats the package: one constant
// with two spellings, so a cookie written through either is read by the other.
const SessionCookieName = session.CookieName

// Errors returned by SessionStore.
var (
	// ErrNoSession means the request carries no session cookie, or the cookie
	// signature does not match the application key.
	ErrNoSession = session.ErrNoSession

	// ErrSessionExpired means the session id is well formed but the backend no
	// longer holds it -- expired, or destroyed by a logout elsewhere.
	//
	// It is session.ErrExpired, and being the same value rather than an equal
	// one is what keeps a backend correct without a line changing: the backend
	// returns this name, hesape/session.Handler requires that one, and there is
	// only ever one value to match.
	ErrSessionExpired = session.ErrExpired

	// ErrConfirmationNotStored means the backend accepted the password
	// confirmation stamp and did not keep it, so no window would ever be
	// satisfied and the password screen would ask again immediately.
	ErrConfirmationNotStored = session.ErrConfirmationNotStored
)

// RememberLifetime is how long a session started with Remember(true) lives.
const RememberLifetime = session.RememberLifetime

// PasswordConfirmationWindow is how long typing the password again counts for.
const PasswordConfirmationWindow = session.PasswordConfirmationWindow

// SessionOption adjusts how a session is started.
type SessionOption = session.Option

// Remember asks for a session that survives closing the browser, for
// RememberLifetime instead of the store's ttl.
func Remember(on bool) SessionOption { return session.Remember(on) }

// PasswordConfirmedWithin reports whether the password was typed again on this
// session less than window ago.
//
// It was a method on Subject and it cannot be one here: Subject is an alias for
// hesape/auth.Subject, and Go does not let a package declare a method on a type
// another package owns. So it is a function, hesape/http's:
//
//	sub.PasswordConfirmedWithin(w)  becomes  security.PasswordConfirmedWithin(sub, w)
func PasswordConfirmedWithin(sub Subject, window time.Duration) bool {
	return hhttp.PasswordConfirmedWithin(sub, window)
}

// SessionBackend stores the subject behind a session id: Get, Put, Delete and
// DeleteSubject. A backend implements it by those four names, and
// NewSessionBackend adapts a hesape/session.Handler to it.
//
// It is hesape/http's under its old name, and the two names are one type.
type SessionBackend = hhttp.SessionBackend

// NewSessionBackend adapts a hesape/session.Handler to a SessionBackend, so a
// Redis or database handler stores the sessions of a SessionStore.
func NewSessionBackend(h session.Handler[Subject]) SessionBackend {
	return hhttp.NewSessionBackend(h)
}

// MemoryBackend keeps sessions in the process memory.
//
// It is the right choice for development and for a single instance. Behind more
// than one pod it silently logs people out on every deploy and on every request
// routed elsewhere -- use a Redis-backed handler through NewSessionBackend.
//
// It is hesape/http's under its old name, and the two names are one type.
type MemoryBackend = hhttp.MemoryBackend

// NewMemoryBackend returns an empty in-memory session backend.
func NewMemoryBackend() *MemoryBackend { return hhttp.NewMemoryBackend() }

// SessionStore issues and validates sessions, and remembers where a guard was
// sending somebody before the sign-in screen.
//
// It is hesape/http's under its old name, and the two names are one type, so
// the guards of hesape/http/middleware and the ones here read the same store.
type SessionStore = hhttp.SessionStore

// NewSessionStore returns a store. Pass secure=false only in development:
// without the Secure attribute the cookie travels over plain HTTP. A nil
// backend keeps the sessions in memory.
func NewSessionStore(appKey []byte, ttl time.Duration, secure bool, b SessionBackend) *SessionStore {
	return hhttp.NewSessionStore(appKey, ttl, secure, b)
}
