// Package security holds the authorization primitives of the framework.
//
// It is not an optional package: Grant, Policy, sessions and password hashing
// live in the core because security is the product thesis, not a plugin the
// user may forget to install.
//
// This package is a bridge. It is removed in v1.0.0; import github.com/arandu-io/hesape/auth directly.
//
// The components moved to github.com/arandu-io/hesape, under the Illuminate
// names, and this package is now the old names pointing at them. It answers to
// five hesape packages, and which one a symbol went to depends on the symbol:
//
//	hesape/auth        Grant, Policy, Subject, Action, Authorize, SystemGrant, the sign-in throttle
//	hesape/session     the session store, the flash, the CSRF token, the cookie name
//	hesape/hashing     HashPassword, VerifyPassword, NeedsRehash
//	hesape/encryption  Signer
//	hesape/http        LocalPath and the intended destination
//
// The death date above is what keeps this from being a second way to import one
// type, which RULE 9 forbids. Nothing here holds an implementation: where the
// name and the signature survived the move it is a Go alias, and where the
// design diverged it is an envelope that translates and nothing more.
//
// The three envelopes, and what diverged:
//
//	SessionStore     hesape/session.RecordStore[Subject] returns a Record that
//	                 wraps the payload, and the intended destination moved to
//	                 hesape/http
//	SessionBackend   hesape/session.Handler renamed all four methods, and
//	                 github.com/arandu-io/kv implements the old names
//	SignInThrottle   all three methods gained a context.Context
package security
