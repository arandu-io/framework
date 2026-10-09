package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"

	"github.com/arandu-io/framework/security"
	hhttp "github.com/arandu-io/hesape/http"
)

// ErrUnknownToken is what a TokenResolver returns for a digest it cannot
// answer with a subject: a token it never issued, one that was revoked, one
// that expired.
//
// It is one error for all of them on purpose. RequireToken answers each of
// them, and a request that carried no token at all, with the same 401, and a
// resolver that told them apart would be telling the guard something the guard
// is not allowed to repeat.
var ErrUnknownToken = errors.New("middleware: unknown bearer token")

// TokenDigest is the SHA-256 of a bearer token, and it is all a TokenResolver
// is ever handed.
//
// The resolver never sees the token, so it has nothing to compare in variable
// time and nothing to write to a log. It looks the digest up -- an indexed
// column holding DigestToken of every token issued -- and a timing difference
// in that lookup can reveal, at most, part of a digest. A digest does not
// authenticate anybody: reaching RequireToken with it means presenting a token
// whose SHA-256 it is, which is what a preimage-resistant hash rules out.
//
// That argument holds for a token nobody can guess, and only for one. A token
// is a random value from crypto/rand, 32 bytes or more; the digest is unsalted
// and fast, which is right for that value and wrong for a password, and it is
// why a token costs a hash per request and not a password hash per request.
type TokenDigest [sha256.Size]byte

// DigestToken returns the digest RequireToken looks token up by.
//
// The code that issues a token calls it once and stores the result -- never the
// token, which is shown to its owner when it is issued and kept nowhere else.
// A stored digest that leaks hands out no credential.
func DigestToken(token string) TokenDigest { return sha256.Sum256([]byte(token)) }

// String returns the digest as 64 lowercase hexadecimal characters, the form a
// text column stores it in.
func (d TokenDigest) String() string { return hex.EncodeToString(d[:]) }

// TokenResolver turns the digest of a bearer token into the subject the token
// was issued to. The application supplies it, because the application is what
// issues, stores, scopes, expires and revokes its tokens.
type TokenResolver interface {
	// ResolveToken returns the subject the token with this digest was issued
	// to, or ErrUnknownToken when there is none -- never issued, revoked or
	// expired. Any other error is a failure to answer, and RequireToken does
	// not turn it into a 401: a token store that is down would otherwise tell
	// every client that its token was taken away.
	//
	// The subject's tenant is the one recorded when the token was issued. It is
	// the only place the request's tenant comes from: nothing on the request
	// names it, and RequireToken reads no header, path or body for it.
	//
	// What the token may do is the subject's Actions, as narrow as the token was
	// issued: a token never carries more than the account that issued it, and
	// the Policy still decides every record.
	ResolveToken(ctx context.Context, digest TokenDigest) (security.Subject, error)
}

// RequireToken refuses a request that does not carry a bearer token the
// resolver knows, and carries the subject of one it does.
//
// The token is read from the Authorization header, scheme Bearer, compared
// case-insensitively, and from nowhere else: a token in a query string reaches
// every log between the client and here, and a token in a cookie is a token a
// browser attaches to a request another site started. There is no fall back to
// the session either, for the second of those reasons -- an API route that took
// a cookie would be a route CSRF reaches.
//
// A request with no token and a request with a token nobody knows are answered
// the same 401, byte for byte, with WWW-Authenticate: Bearer and nothing that
// says which of the two it was. A client that wants JSON gets a problem
// document; anything else gets the status and its sentence.
//
// A request it lets through carries the subject the resolver returned, on the
// request context, so the handler reads it with Context.User or
// auth.SubjectFrom -- exactly as it does behind RequireAuth. That is who is
// asking and nothing more: whether they may touch a record is still the
// Policy's answer, and the Grant it issues is never put on the context.
//
// The subject replaces any subject already on the request, rather than being
// kept beside it the way RequireAuth keeps one for the same account: a token is
// usually narrower than the session of the account that issued it, and a
// cookie that happened to ride along must not widen what the token may do.
//
// A subject with no id is not somebody a token can name -- a declared guest is
// one, since security.Guest sets no id -- and is refused as an unknown token.
func RequireToken(tokens TokenResolver) func(http.Handler) http.Handler {
	if tokens == nil {
		panic("middleware: RequireToken was given a nil TokenResolver. Pass the application's resolver; there is no guard that checks a token against nothing")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := hhttp.NewContext(w, r, nil, nil).BearerToken()
			if token == "" {
				refuseToken(w, r)
				return
			}
			subject, err := tokens.ResolveToken(r.Context(), DigestToken(token))
			if errors.Is(err, ErrUnknownToken) {
				refuseToken(w, r)
				return
			}
			if err != nil {
				// Not a refusal: the resolver could not answer. It goes to the
				// panic path, which answers 500 and logs the cause, rather than
				// telling a client with a good token that it has none.
				panic(err)
			}
			if subject.ID == "" {
				refuseToken(w, r)
				return
			}
			next.ServeHTTP(w, carrySubject(r, subject))
		})
	}
}

// refuseToken is the one answer to a request whose token was missing, unknown
// or named nobody. One function, so that the three cannot drift apart into an
// answer that tells them apart.
func refuseToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	refuse(w, r, http.StatusUnauthorized, "a valid bearer token is required")
}
