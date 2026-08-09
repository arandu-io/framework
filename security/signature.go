package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Signer issues links that prove something without storing anything.
//
// It is what an e-mail verification link is made of, and what an unsubscribe
// link should be made of. The alternative -- a table of tokens -- costs a write,
// a read, a cleanup job and a decision about what happens when the row is gone;
// this costs a signature, and a link that has expired says so rather than saying
// "unknown token" three months after the row was deleted.
//
// Two properties are what make it safe to put in a URL:
//
//   - The purpose is signed. A token issued to verify an address does not work
//     on a password reset, even though both are signed with the same key. Reusing
//     one for the other is the mistake this prevents, and it is not an unlikely
//     one: both are "a link in an e-mail with an id in it".
//   - The expiry is signed. Moving it is changing the payload, so a link that
//     has run out cannot be extended by editing the URL.
//
// What it deliberately does NOT do is make the link single-use. That needs
// state, and where single use matters -- a password reset -- the state is the
// password itself: the token carries the current hash, so using the link changes
// what it was signed against. See ADR 0032.
type Signer struct {
	key []byte
}

// ErrSignature is what every failure below unwraps to, so a caller answers "this
// link is not valid" once rather than switching on four reasons it is not.
var ErrSignature = errors.New("security: the signature is not valid")

// NewSigner returns a Signer over the application key.
//
// The same key as the session and the CSRF token, because they are the same
// secret: an attacker who has it does not need three.
func NewSigner(appKey []byte) *Signer { return &Signer{key: appKey} }

// Sign returns a token carrying payload, valid for ttl, usable only for purpose.
//
// The payload is not secret -- it is base64 in a URL, and anyone can read it.
// What the signature buys is that nobody can change it.
func (s *Signer) Sign(purpose, payload string, ttl time.Duration) string {
	expires := time.Now().Add(ttl).Unix()
	body := base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + strconv.FormatInt(expires, 10)
	return body + "." + s.sign(purpose, body)
}

// Verify checks a token and returns what was signed into it.
//
// The order matters: the signature is checked before the expiry is read, because
// an unsigned expiry is a number the client chose.
func (s *Signer) Verify(purpose, token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("%w: it is not a token", ErrSignature)
	}
	body := parts[0] + "." + parts[1]

	// hmac.Equal and not ==: a comparison that returns early tells an attacker
	// how much of a forged signature was right.
	if !hmac.Equal([]byte(s.sign(purpose, body)), []byte(parts[2])) {
		return "", fmt.Errorf("%w: it was not issued by this application, or not for %s", ErrSignature, purpose)
	}

	expires, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "", fmt.Errorf("%w: the expiry is unreadable", ErrSignature)
	}
	if time.Now().Unix() > expires {
		// Named separately from every other failure, because it is the one a
		// person can act on: "ask for another one" rather than "this is broken".
		return "", ErrExpired
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", fmt.Errorf("%w: the payload is unreadable", ErrSignature)
	}
	return string(payload), nil
}

// ErrExpired is a valid signature that has run out of time. It unwraps to
// ErrSignature, so a caller that does not care about the difference does not
// have to check for both -- and one that does can offer a new link.
var ErrExpired = fmt.Errorf("%w: the link has expired", ErrSignature)

func (s *Signer) sign(purpose, body string) string {
	m := hmac.New(sha256.New, s.key)
	// The purpose is written with its length in front of it, so that a purpose
	// of "verify" with a body of "x.y" cannot produce the same input as a
	// purpose of "verifyx" with a body of ".y". Without it the two are the same
	// byte string, and one token works for both.
	_, _ = fmt.Fprintf(m, "%d:%s|%s", len(purpose), purpose, body)
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
