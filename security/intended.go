// The intended destination, answered by github.com/arandu-io/hesape/http.
//
// It is Routing\Redirector::guest() and ::intended(), so it moved to the
// package that answers a request rather than to the one that holds a session:
// the value it carries is an address, and the whole of its correctness is
// LocalPath.
//
// The two methods that read and write it are on SessionStore, in session.go,
// where they have always been.

package security

import hhttp "github.com/arandu-io/hesape/http"

// IntendedCookieName carries the address somebody was going to, between the
// request a guard turned away and the sign-in that follows it.
const IntendedCookieName = hhttp.IntendedCookieName

// IntendedLifetime is how long the address is worth keeping.
//
// Long enough to find a password in a manager and type it, and short enough
// that it does not survive to an unrelated sign-in.
const IntendedLifetime = hhttp.IntendedLifetime
