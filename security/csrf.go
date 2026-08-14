// The CSRF token, answered by github.com/arandu-io/hesape/session -- the token
// is Session\Store::token() in Laravel, and what binds it is the session id.

package security

import (
	"time"

	"github.com/arandu-io/hesape/session"
)

// ErrCSRF means the token is invalid, expired, or bound to another session.
//
// Renamed on the way to hesape: it is session.ErrTokenMismatch there, after
// Illuminate's TokenMismatchException.
var ErrCSRF = session.ErrTokenMismatch

// CSRF issues double-submit tokens signed with HMAC and bound to the session.
// It keeps no server-side state: the token carries its own expiry, so a
// deployment does not need Redis just to protect forms.
type CSRF = session.CSRF

// NewCSRF returns a token issuer keyed by the application key.
func NewCSRF(appKey []byte, ttl time.Duration) *CSRF { return session.NewCSRF(appKey, ttl) }
