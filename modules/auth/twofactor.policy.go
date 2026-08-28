package auth

import (
	"context"
	"fmt"

	"github.com/arandu-io/framework/security"
)

// The second factor's actions. Two and not one, because reading the enrolment
// and changing it are different powers: whoever reads the secret produces codes
// forever, and whoever changes it can only take the factor away.
const (
	// ActionUserTwoFactorRead is reading the enrolment, secret included.
	ActionUserTwoFactorRead security.Action = "auth.user.two_factor.read"

	// ActionUserTwoFactorManage is enrolling, confirming, disabling, reissuing
	// recovery codes and recording a spent time step.
	ActionUserTwoFactorManage security.Action = "auth.user.two_factor.manage"
)

// TwoFactorPolicy is the only authority over who does what with a [TwoFactor].
//
// It denies by default: the switch has no branch that allows without naming a
// condition first.
type TwoFactorPolicy struct{}

// Can decides whether the subject may perform the action on the enrolment.
//
// # An administrator is not on this list, and that is the decision
//
// Everywhere else in this module "the account's owner or an administrator" is
// the rule, and here it is the owner alone. An administrator who could read the
// secret would produce that account's codes at will, and one who could disable
// the factor would hold, together with the password reset they already have,
// every step of signing in as somebody else -- which is the exact attack a
// second factor is bought to stop. The support case behind the temptation is
// real: somebody loses the phone and the printed codes. It is answered at a
// terminal, by a person, the way [Service.SetPassword] answers the case of a
// reset link that cannot be delivered -- not by a role that every incident
// hands out.
func (TwoFactorPolicy) Can(ctx context.Context, s security.Subject, a security.Action, t TwoFactor) error {
	// Tenant isolation comes first and applies to every action: without it,
	// every check below would be pointless in a multi-tenant system.
	if t.TenantID != "" && t.TenantID != s.Tenant {
		return fmt.Errorf("resource belongs to another tenant")
	}

	switch a {
	case ActionUserTwoFactorRead, ActionUserTwoFactorManage:
		// The enrolment names its owner even before a row exists: the candidate
		// a use case authorizes carries the subject's own id, so an enrolment
		// begun for somebody else is refused here rather than by the statement
		// that would have written it.
		if s.ID != "" && s.ID == t.UserID {
			return nil
		}
	}
	return fmt.Errorf("insufficient role for %s", a)
}

// Compile-time proof that the policy honors the contract the framework checks
// grants against.
var _ security.Policy[TwoFactor] = TwoFactorPolicy{}
