// The bearer token guard, answered by github.com/arandu-io/hesape/http/middleware.

package middleware

import (
	"net/http"

	hmiddleware "github.com/arandu-io/hesape/http/middleware"
)

// ErrUnknownToken is what a TokenResolver returns for a digest it cannot
// answer with a subject: a token it never issued, one that was revoked, one
// that expired. RequireToken answers each of them with the same 401.
var ErrUnknownToken = hmiddleware.ErrUnknownToken

// TokenDigest is the SHA-256 of a bearer token, and it is all a TokenResolver
// is ever handed: the store keeps digests, never tokens.
type TokenDigest = hmiddleware.TokenDigest

// DigestToken is the digest of a bearer token, the one a TokenResolver is
// handed and the one the store keeps when the token is issued.
func DigestToken(token string) TokenDigest { return hmiddleware.DigestToken(token) }

// TokenResolver answers the subject a token digest was issued to, or
// ErrUnknownToken.
type TokenResolver = hmiddleware.TokenResolver

// RequireToken lets a request carrying a known bearer token through, with the
// subject it was issued to on the context, and answers anything else 401.
func RequireToken(tokens TokenResolver) func(http.Handler) http.Handler {
	return hmiddleware.RequireToken(tokens)
}
