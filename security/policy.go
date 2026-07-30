// Package security holds the authorization primitives of the framework.
//
// It is not an optional package: Grant, Policy, sessions and password hashing
// live in the core because security is the product thesis, not a plugin the
// user may forget to install.
package security

import (
	"context"
	"errors"
	"fmt"
)

// ErrForbidden is the only authorization error. Handlers translate it to 403.
var ErrForbidden = errors.New("arandu: action not authorized")

// Subject is whoever is acting. It comes from the session, never from the
// request body.
type Subject struct {
	ID     string
	Tenant string
	Roles  []string
}

// HasRole reports whether the subject carries the given role.
func (s Subject) HasRole(r string) bool {
	for _, have := range s.Roles {
		if have == r {
			return true
		}
	}
	return false
}

// Action is the intended operation, in "module.verb" form.
type Action string

// Policy decides. One policy per entity, always in the module's
// <entity>.policy.go file -- the CLI generates the skeleton and `aru doctor`
// complains when a repository exists without a matching policy.
type Policy[T any] interface {
	// Can decides whether subject may perform action on resource. resource may
	// be the zero value for collection actions (e.g. "customer.list").
	Can(ctx context.Context, s Subject, a Action, resource T) error
}

// Grant is the proof that an authorization decision happened.
//
// THIS IS THE CENTRAL PIECE OF THE FRAMEWORK. Grant has only unexported fields
// and no public constructor other than Authorize. Because every repository
// signature requires a Grant, reaching the database without going through a
// Policy is IMPOSSIBLE -- not "discouraged", impossible at compile time.
//
// This is what Laravel does not have: there, the Gate is a call you can simply
// forget to make, and nothing warns you.
type Grant struct {
	subject Subject
	action  Action
	valid   bool
}

// Authorize runs the policy and, when allowed, issues the Grant.
func Authorize[T any](ctx context.Context, p Policy[T], s Subject, a Action, resource T) (Grant, error) {
	if s.ID == "" {
		return Grant{}, fmt.Errorf("%w: anonymous subject on %s", ErrForbidden, a)
	}
	if err := p.Can(ctx, s, a, resource); err != nil {
		return Grant{}, fmt.Errorf("%w: %s denied for subject %s: %v", ErrForbidden, a, s.ID, err)
	}
	return Grant{subject: s, action: a, valid: true}, nil
}

// Check is the guard every repository operation must call.
//
// It fails on the zero value -- the only Grant a caller outside this package
// can build -- and when the grant was issued for a different action, which
// catches copy-paste between repository methods.
func (g Grant) Check(expected Action) error {
	if !g.valid {
		return fmt.Errorf("%w: missing grant for %s (call security.Authorize first)", ErrForbidden, expected)
	}
	if g.action != expected {
		return fmt.Errorf("%w: grant issued for %s, used on %s", ErrForbidden, g.action, expected)
	}
	return nil
}

// Subject exposes who was authorized -- used to scope SQL by tenant.
func (g Grant) Subject() Subject { return g.subject }

// Action exposes what was authorized.
func (g Grant) Action() Action { return g.action }

// SystemGrant exists for jobs and migrations that run outside a request, and
// for the login path, where there is no subject yet.
//
// Every call site is auditable: `aru doctor --strict` lists them all.
func SystemGrant(a Action, tenant string) Grant {
	return Grant{
		subject: Subject{ID: "system", Tenant: tenant, Roles: []string{"system"}},
		action:  a,
		valid:   true,
	}
}
