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
	"regexp"
)

// ErrForbidden is the only authorization error. Handlers translate it to 403.
var ErrForbidden = errors.New("arandu: action not authorized")

// Subject is whoever is acting. It comes from the session, never from the
// request body.
type Subject struct {
	ID     string
	Tenant string
	Roles  []string

	// guest marks a subject that is deliberately anonymous. It is unexported and
	// only Guest sets it, which is the whole point: a Subject nobody filled in
	// is not a guest, it is a session somebody forgot to load, and Authorize
	// tells those two apart.
	guest bool
}

// Guest is a reader with no session, declared on purpose.
//
// It exists because a public page is a real requirement and the alternative was
// worse. Authorize refuses an empty subject before it consults a policy -- which
// is right, because an empty subject is almost always a forgotten session load
// -- and that left no way at all to say "anybody may read a published post".
// The only path was security.SystemGrant, which skips the policy entirely: a
// blog served with the same instrument a scheduled job uses.
//
// So the refusal stays and the exception is explicit. A zero Subject is still
// refused. This one reaches the policy, and the POLICY decides:
//
//	func (PostPolicy) Can(ctx context.Context, s security.Subject, a security.Action, p models.Post) error {
//		if s.IsGuest() {
//			if a == PostView && !p.PublishedAt.IsZero() {
//				return nil
//			}
//			return fmt.Errorf("%s is not public", a)
//		}
//		…
//	}
//
// Nothing is loosened by this. Authorization still happens in one place, the
// Grant is still the only way to a repository, and a policy that says nothing
// about guests denies them -- which is what every generated policy does, so the
// default is closed.
//
// The tenant is required and is the application's, from configuration. A
// visitor cannot choose whose rows they read, and RULE 14 is not suspended
// because nobody signed in.
func Guest(tenant string) Subject {
	return Subject{Tenant: tenant, guest: true}
}

// IsGuest reports whether this subject is a declared anonymous reader.
//
// A policy that never asks denies them, because it will fall through to its
// final refusal -- HasRole answers false for a guest, and there is no id to
// compare an owner against.
func (s Subject) IsGuest() bool { return s.guest }

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
// THIS IS THE CENTRAL PIECE OF THE FRAMEWORK. Grant has only unexported fields,
// so it cannot be built by writing a struct literal: every repository signature
// requires one, and reaching the database without a Grant does not compile.
//
// What the compiler does NOT decide is which Grant. Authorize is the mandatory
// path and the only one where a Policy answered; SystemGrant is the named
// escape hatch and jobs.GrantFor wraps it, and both are exported, so a handler
// can construct a Grant nobody authorized. What stops that is `aru doctor` --
// a lint, not the type system -- with system-grant-outside-scope,
// system-grant-without-tenant and tenant-from-request.
//
// This comment used to say "no public constructor other than Authorize", which
// was never true and read as a compile-time guarantee for something a lint
// enforces. It is the difference between the promise and the mechanism, and
// stating it wrong here is worse than anywhere else: this is the doc a reader
// checks the thesis against.
//
// The alternative shape -- authorization as a call the handler remembers to make
// -- is authorization that gets forgotten, and nothing warns you.
type Grant struct {
	subject Subject
	action  Action
	valid   bool
	// reason is why an invalid Grant is invalid, when something knew.
	//
	// The zero Grant carries none, and its message is the right one for it: a
	// caller who never authorized anything is told to. A Grant refused by
	// SystemGrant is a different mistake, and Check says which.
	reason string
}

// Authorize runs the policy and, when allowed, issues the Grant.
func Authorize[T any](ctx context.Context, p Policy[T], s Subject, a Action, resource T) (Grant, error) {
	// An empty subject is refused before the policy is asked, because it is
	// almost always a session that was not loaded -- and a policy asked about
	// nobody answers about nobody.
	//
	// A Guest is the exception, and it is an exception the caller declared: it
	// carries a marker only security.Guest sets. The policy decides about it
	// like any other subject, and a policy that says nothing about guests
	// refuses them.
	if s.ID == "" && !s.guest {
		return Grant{}, fmt.Errorf("%w: anonymous subject on %s", ErrForbidden, a)
	}
	if err := p.Can(ctx, s, a, resource); err != nil {
		who := s.ID
		if s.guest {
			who = "a guest"
		}
		return Grant{}, fmt.Errorf("%w: %s denied for subject %s: %v", ErrForbidden, a, who, err)
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
		// A refused SystemGrant says why it was refused. It used to fall through
		// to the message below, which tells the caller to call Authorize -- and
		// in a job or a scheduled task there is no request to authorize from, so
		// the advice is impossible to follow and points away from the real
		// cause, which is the tenant. Found by audit.
		if g.reason != "" {
			return fmt.Errorf("%w: %s", ErrForbidden, g.reason)
		}
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

// SystemGrant exists for jobs that run outside a request, and for the login
// path, where there is no subject yet.
//
// The tenant is required and cannot be empty. A system grant without a tenant
// would read across every customer of the system, which in a SaaS is the worst
// bug there is -- so it is not expressible: an empty tenant yields the zero
// Grant, and the zero Grant fails Check.
//
// Every call site is auditable, and `aru doctor` reports the ones outside a
// seeder, a job or a command. `--strict` does not list them -- it turns that
// warning into a failure, which is what CI runs.
// tenantName is what a tenant identifier may contain.
//
// Closed on purpose. A tenant is concatenated into a storage path, a cache key,
// a scheduler lock name and a queue key -- so a tenant carrying "/" or ":"
// collides with another tenant's namespace, and one carrying ".." leaves it.
//
// Found by audit: tenant "acme/reports" storing key "q1.pdf" and tenant "acme"
// storing "reports/q1.pdf" resolved to the same object, each holding a
// perfectly valid Grant of its own. No Policy was violated -- the path is built
// after the Policy runs.
//
// UUIDs, slugs and numeric ids all pass. Anything that could be read as a
// separator does not.
var tenantName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// ValidTenant reports whether a tenant identifier is safe to use as a namespace.
//
// Exported because the adapters build keys from it and a second, slightly
// different definition in each of them is how one of them ends up permissive.
func ValidTenant(tenant string) bool { return tenantName.MatchString(tenant) }

func SystemGrant(a Action, tenant string) Grant {
	// An invalid tenant produces the zero Grant, which passes no Check -- the
	// same answer an empty one has always produced, for the same reason: a
	// tenant that cannot be trusted as a namespace cannot scope anything.
	if !ValidTenant(tenant) {
		if tenant == "" {
			return Grant{reason: fmt.Sprintf(
				"a system grant for %s was asked for with no tenant. Nothing can be scoped without one, and a query that is not scoped reads every customer. The tenant comes from the job, the task or the row that caused this work", a)}
		}
		return Grant{reason: fmt.Sprintf(
			"a system grant for %s was asked for with the tenant %q, which cannot be one: a tenant is concatenated into a storage path, a cache key and a lock name, so it is limited to letters, digits, - and _, up to 64 characters", a, tenant)}
	}
	return Grant{
		subject: Subject{ID: "system", Tenant: tenant, Roles: []string{"system"}},
		action:  a,
		valid:   true,
	}
}
