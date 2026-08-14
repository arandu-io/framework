// The flash, answered by github.com/arandu-io/hesape/session, and LocalPath,
// answered by github.com/arandu-io/hesape/http -- it validates an address, and
// hesape/session never validates a URL.

package security

import (
	hhttp "github.com/arandu-io/hesape/http"
	"github.com/arandu-io/hesape/session"
)

// FlashCookieName carries the messages and the typed input of a rejected
// request across the one redirect that follows it.
const FlashCookieName = session.FlashCookieName

// FlashLifetime is how long the messages are worth keeping.
const FlashLifetime = session.FlashLifetime

// MaxFlashBytes is the budget for the signed cookie value.
const MaxFlashBytes = session.MaxFlashBytes

// Flash carries the messages and the input of a rejected request across the one
// redirect that follows it.
type Flash = session.Flash

// NewFlash returns a Flash over the application key.
func NewFlash(appKey []byte, secure bool) *Flash { return session.NewFlash(appKey, secure) }

// LocalPath reports whether an address stays inside this application, and
// returns it when it does.
//
// It is the open-redirect defence, and it is one function in the collection
// rather than one per caller: httpx.Reject calls it on the address a rejected
// form is sent back to, which comes off the Referer header and is therefore the
// visitor's to choose, and the intended destination calls it twice around a
// signed cookie.
func LocalPath(raw string) (string, bool) { return hhttp.LocalPath(raw) }
