// The rate limit keys. The limit itself is answered by
// github.com/arandu-io/hesape/routing/middleware.Throttle, which counts in a
// store rather than in this process.

package middleware

import (
	"net/http"

	hmiddleware "github.com/arandu-io/hesape/http/middleware"
	rmiddleware "github.com/arandu-io/hesape/routing/middleware"
)

// KeyByIP keys on the peer address: the whole address over IPv4, and the /64 it
// sits in over IPv6. It reads RemoteAddr and never X-Forwarded-For: a header the
// client controls is a way to reset someone else's counter.
//
// It is hesape/routing/middleware.KeyByIP, and the key is byte for byte that
// function's.
func KeyByIP(r *http.Request) string { return rmiddleware.KeyByIP(r) }

// Sessions is what KeyBySession reads a session through. *security.SessionStore
// satisfies it.
type Sessions = hmiddleware.Sessions

// KeyBySession keys on the session id while the store still holds that
// session, and on the address otherwise. Pass the application's
// *security.SessionStore.
func KeyBySession(sessions Sessions) func(*http.Request) string {
	return hmiddleware.KeyBySession(sessions)
}
