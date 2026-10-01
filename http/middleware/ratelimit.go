// The rate limit keys. The limit itself is answered by
// github.com/arandu-io/hesape/routing/middleware.Throttle, which counts in a
// store rather than in this process.

package middleware

import (
	"context"
	"net/http"

	"github.com/arandu-io/framework/security"
	rmiddleware "github.com/arandu-io/hesape/routing/middleware"
)

// KeyByIP keys on the peer address: the whole address over IPv4, and the /64 it
// sits in over IPv6.
//
// It reads RemoteAddr and never X-Forwarded-For: a header the client controls is
// a way to reset someone else's counter. Behind a proxy, have the proxy rewrite
// RemoteAddr, or key on something the proxy signs. A proxy that does neither
// gives every request in the world the same key, and then every limit keyed this
// way is a limit on the whole application -- which for the sign-in throttle
// means twenty-five wrong passwords a minute across every customer.
//
// # Why the IPv6 address is masked
//
// Because otherwise it is not a limit. IPv4 addresses are scarce, so keying on
// the whole address costs an attacker money; a /64 is the smallest block any end
// site is given -- a home connection, a VPS, a phone -- and every one of them
// holds eighteen quintillion addresses that all reach this server. Keyed on the
// full address, one machine with a routed /64 had an unlimited number of
// budgets: it could walk a list of accounts forever, and fill the sign-in
// throttle's table on its own, from a single upstream link.
//
// The /64 and not something wider, because it is the one boundary that is
// always a single link. A /48 would be one customer at some providers and a
// whole building at others, and grouping two subscribers under one budget is
// how a limit locks out somebody who did nothing.
//
// A wrapper and not an alias, because a plain function has no alias form. The
// key it returns is byte-for-byte the one this package produced before the
// move, which matters: a counter in a shared store is keyed by this string, and
// a different prefix would hand every caller a fresh budget on deploy.
func KeyByIP(r *http.Request) string { return rmiddleware.KeyByIP(r) }

// Sessions is what KeyBySession reads a session through. *security.SessionStore
// satisfies it.
type Sessions interface {
	// IDFromRequest is the session id the request's cookie names, once its
	// signature is verified, or the empty string.
	IDFromRequest(r *http.Request) string
	// Load returns the subject of the request's session, and an error when the
	// store does not hold it.
	Load(ctx context.Context, r *http.Request) (security.Subject, error)
}

// KeyBySession keys on the session id while the store still holds that
// session, and on the address otherwise. Pass the application's
// *security.SessionStore.
//
// The cookie alone is not enough to key on. Its signature proves the id was
// issued here once, and every expired or signed-out id a client kept would be
// a fresh budget of its own; asking the store closes that, so a session that
// is gone falls back to the address like a request with no cookie at all.
//
// The key is byte for byte the one the counter is kept under, and a store that
// cannot answer is a session that does not exist: the request is counted by
// its address rather than let through.
//
// The declared return type stays func(*http.Request) string rather than
// hesape's named KeyFunc; the value returned is the same function either way.
func KeyBySession(sessions Sessions) func(*http.Request) string {
	return func(r *http.Request) string {
		return rmiddleware.KeyBySession(liveSession{sessions: sessions, r: r})(r)
	}
}

// liveSession answers the two questions the counter asks with the two methods
// the session store declares. It is built per request because Load reads the
// session from the request rather than from an id.
type liveSession struct {
	sessions Sessions
	r        *http.Request
}

func (s liveSession) ID(r *http.Request) string { return s.sessions.IDFromRequest(r) }

func (s liveSession) Exists(ctx context.Context, _ string) bool {
	_, err := s.sessions.Load(ctx, s.r)
	return err == nil
}
