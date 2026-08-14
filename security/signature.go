// Signed links, answered by github.com/arandu-io/hesape/encryption.
//
// Not by hesape/routing, where a signed route would seem to belong: routing
// imports http, which imports session, which imports cookie, which imports
// routing. The Signer holds the application key and nothing else, and the key
// is encryption's.

package security

import "github.com/arandu-io/hesape/encryption"

// ErrSignature is what every signature failure unwraps to, so a caller answers
// "this link is not valid" once rather than switching on four reasons it is not.
var ErrSignature = encryption.ErrSignature

// ErrExpired is a valid signature that has run out of time. It unwraps to
// ErrSignature, so a caller that does not care about the difference does not
// have to look at it.
var ErrExpired = encryption.ErrExpired

// Signer issues links that prove something without storing anything.
//
// It is what an e-mail verification link is made of. The purpose and the expiry
// are both signed, which is what makes it safe to put in a URL.
type Signer = encryption.Signer

// NewSigner returns a Signer over the application key.
func NewSigner(appKey []byte) *Signer { return encryption.NewSigner(appKey) }
