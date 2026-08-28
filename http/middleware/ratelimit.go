// The rate limit keys. The limit itself is answered by
// github.com/arandu-io/hesape/routing/middleware.Throttle, which counts in a
// store rather than in this process.

package middleware

import (
	"net/http"

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

// KeyBySession keys on the session id, falling back to the address for
// anonymous requests. Pass SessionStore.IDFromRequest as the extractor.
//
// The declared return type stays func(*http.Request) string rather than
// hesape's named KeyFunc, so the signature every caller is written against is
// unchanged; the value returned is the same function either way.
func KeyBySession(idFrom func(*http.Request) string) func(*http.Request) string {
	return rmiddleware.KeyBySession(idFrom)
}
