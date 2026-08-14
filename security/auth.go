// The authorization core, answered by github.com/arandu-io/hesape/auth.
//
// Every name here is an alias or a one-line call through. The Grant, the
// Policy and the Subject a project writes against are literally the hesape
// types, so a repository generated against this package and one generated
// against hesape/auth take the same Grant.

package security

import (
	"context"

	"github.com/arandu-io/hesape/auth"
)

// ErrForbidden is the only authorization error. Handlers translate it to 403.
var ErrForbidden = auth.ErrForbidden

// Subject is whoever is acting. It comes from the session, never from the
// request body.
//
// PasswordConfirmedWithin is no longer a method on it: see the package-level
// function of that name, and the note there for why.
type Subject = auth.Subject

// Action is the intended operation, in "module.verb" form.
type Action = auth.Action

// Policy decides. One policy per entity, always in the module's
// <entity>.policy.go file.
//
// Generic types alias, so a policy written against this name satisfies
// hesape/auth.Policy without being rewritten.
type Policy[T any] = auth.Policy[T]

// Grant is the proof that an authorization decision happened.
//
// THIS IS THE CENTRAL PIECE OF THE FRAMEWORK, and the alias is what keeps it
// one type rather than two: a Grant minted by hesape/auth is the same value a
// repository written against this package checks.
type Grant = auth.Grant

// The sign-in policy. Three constants and not configuration, for the reason
// stated on hesape/auth: a lockout somebody can widen from the environment is a
// lockout somebody widens the morning it fires.
const (
	MaxSignInFailures          = auth.MaxSignInFailures
	MaxSignInFailuresPerClient = auth.MaxSignInFailuresPerClient
	SignInWindow               = auth.SignInWindow
)

// Authorize runs the policy and, when allowed, issues the Grant.
//
// A wrapper and not an alias: Go has no alias form for a generic function.
func Authorize[T any](ctx context.Context, p Policy[T], s Subject, a Action, resource T) (Grant, error) {
	return auth.Authorize(ctx, p, s, a, resource)
}

// Guest is a reader with no session, declared on purpose.
func Guest(tenant string) Subject { return auth.Guest(tenant) }

// ValidTenant reports whether a tenant identifier is safe to use as a namespace.
func ValidTenant(tenant string) bool { return auth.ValidTenant(tenant) }

// SystemGrant exists for jobs that run outside a request, and for the login
// path, where there is no subject yet.
//
// A wrapper and not a var, deliberately: an exported function variable holding
// the framework's one escape hatch is a value any package in the build can
// reassign at init, and this is the last symbol in the collection that should
// be reachable that way.
func SystemGrant(a Action, tenant string) Grant { return auth.SystemGrant(a, tenant) }
