package auth

import (
	"context"
	"fmt"

	"github.com/arandu-io/framework/security"
)

// Actions of this module. They are constants, not strings at the call site: a
// typo in an action name would silently authorize nothing, or worse, everything.
const (
	ActionUserView   security.Action = "auth.user.view"
	ActionUserCreate security.Action = "auth.user.create"
	ActionUserUpdate security.Action = "auth.user.update"
	ActionUserDelete security.Action = "auth.user.delete"
)

// UserPolicy is the only authority over who does what with a User.
//
// It denies by default: the switch has no default branch that allows. `aru
// doctor` fails when a repository exists without a matching policy.
type UserPolicy struct{}

// Can decides whether the subject may perform the action on the user.
func (UserPolicy) Can(ctx context.Context, s security.Subject, a security.Action, u User) error {
	// Tenant isolation comes first and applies to every action: without it,
	// every check below would be pointless in a multi-tenant system.
	if u.ID != "" && u.TenantID != s.Tenant {
		return fmt.Errorf("resource belongs to another tenant")
	}

	switch a {
	case ActionUserView:
		if s.ID == u.ID || s.HasRole("admin") {
			return nil
		}
	case ActionUserUpdate:
		if s.ID == u.ID || s.HasRole("admin") {
			return nil
		}
	case ActionUserCreate, ActionUserDelete:
		if s.HasRole("admin") {
			return nil
		}
	}
	return fmt.Errorf("insufficient role for %s", a)
}
