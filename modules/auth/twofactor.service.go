package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/observability"
	"github.com/arandu-io/framework/security"
	twofactor "github.com/arandu-io/hesape/2fa"
	"github.com/arandu-io/hesape/otp"
)

// BeginTwoFactorEnrolment produces a secret for the account to scan, and stores
// it unconfirmed.
//
// It turns nothing on. What comes back is what the screen shows once -- the
// provisioning URI a QR code encodes, and the same secret as text for when the
// camera will not focus -- and the account is protected by exactly what it was
// protected by before. [Service.ConfirmTwoFactorEnrolment] is what makes the
// factor real, and it is deliberately a second request: a secret that gated
// sign-in the moment it was written would lock out every person whose scan
// failed, and there would be no way left for them to say so.
//
// Starting the flow again replaces an unconfirmed secret, which is what
// somebody who abandoned the screen and came back needs. It does not replace a
// confirmed one: that would leave the account behind the password alone until
// the new secret was confirmed, with nothing on the screen saying so, and it is
// refused with [ErrTwoFactorAlreadyEnabled]. Turning the factor off is
// [Service.DisableTwoFactor], where it is a decision somebody made rather than
// a side effect of pressing a button twice.
//
// The issuer is what the person reads at the top of the entry in their
// authenticator, so it is the application's name and it is a parameter: this
// module does not know what the application is called, and a wrong name here is
// three identical unnamed entries on the phone of somebody with three accounts.
//
// No event is published. There is nothing yet for anything to react to, and an
// event at this point would have to be named for a state the account is not in.
func (s *Service) BeginTwoFactorEnrolment(ctx context.Context, actor security.Subject, issuer string) (TwoFactorEnrolment, error) {
	manage, err := security.Authorize(ctx, s.twoFactorPolicy, actor, ActionUserTwoFactorManage,
		TwoFactor{UserID: actor.ID, TenantID: actor.Tenant})
	if err != nil {
		return TwoFactorEnrolment{}, err
	}
	u, err := s.selfForTwoFactor(ctx, actor)
	if err != nil {
		return TwoFactorEnrolment{}, err
	}

	secret := otp.NewSecret()
	// The address and not the id: the account name is what tells somebody with
	// three accounts on this service which entry is which, and an identifier
	// only the database understands tells them nothing.
	uri, err := twofactor.Provisioning{Issuer: issuer, Account: u.Email, Secret: secret}.URI()
	if err != nil {
		return TwoFactorEnrolment{}, err
	}

	if err := data.Transaction(ctx, s.repo.db, func(ctx context.Context) error {
		_, err := s.secondFactor.Enrol(ctx, manage,
			TwoFactor{UserID: u.ID, Secret: otp.EncodeSecret(secret)})
		return err
	}); err != nil {
		return TwoFactorEnrolment{}, err
	}
	return TwoFactorEnrolment{SecretKey: otp.EncodeSecret(secret), URI: uri}, nil
}

// ConfirmTwoFactorEnrolment checks the first code and turns the second factor
// on, returning the recovery codes.
//
// The code is what proves an authenticator holds the same secret, and until it
// arrives the enrolment gates nothing. That is the whole reason enrolment takes
// two steps.
//
// The codes come back in plain text, once, and are never readable again: what
// is stored is a password hash of each. A caller shows them, and whoever does
// not write them down has to reissue.
//
// # The order of the writes
//
// The code is verified before the transaction opens, and verifying it spends
// its time step. A failure after that point therefore leaves the step burned
// and the factor still off, so the person confirms with the next code rather
// than the one they just typed. That is the safe direction: the alternative is
// a step that is spendable twice, which is the property the whole replay guard
// exists to deny.
//
// The recovery codes are hashed before the transaction as well, for the reason
// [Service.SetPassword] hashes before its own: a password hash is a tenth of a
// second, and eight of them is eight tenths of a second that every other writer
// of this account's rows would spend waiting.
func (s *Service) ConfirmTwoFactorEnrolment(ctx context.Context, actor security.Subject, code string) ([]string, error) {
	candidate := TwoFactor{UserID: actor.ID, TenantID: actor.Tenant}
	read, err := security.Authorize(ctx, s.twoFactorPolicy, actor, ActionUserTwoFactorRead, candidate)
	if err != nil {
		return nil, err
	}
	manage, err := security.Authorize(ctx, s.twoFactorPolicy, actor, ActionUserTwoFactorManage, candidate)
	if err != nil {
		return nil, err
	}
	u, err := s.selfForTwoFactor(ctx, actor)
	if err != nil {
		return nil, err
	}

	enrolment, err := s.secondFactor.Find(ctx, read, u.ID)
	if err != nil {
		return nil, err
	}
	if enrolment.Enabled() {
		return nil, ErrTwoFactorAlreadyEnabled
	}
	secret, err := enrolment.SecretBytes()
	if err != nil {
		return nil, err
	}

	guard := twoFactorReplayGuard{repo: s.secondFactor, grant: manage}
	if err := (twofactor.Authenticator{Guard: guard}).Verify(ctx, u.ID, secret, code); err != nil {
		return nil, err
	}

	codes, hashes, err := newRecoveryCodes()
	if err != nil {
		return nil, err
	}

	if err := data.Transaction(ctx, s.repo.db, func(ctx context.Context) error {
		confirmed, err := s.secondFactor.Confirm(ctx, manage, u.ID, time.Now().UTC())
		if err != nil {
			return err
		}
		if !confirmed {
			// Somebody confirmed between the read above and this statement. The
			// database is the referee, so exactly one caller is told it turned
			// the factor on -- and this one is told what is true.
			return ErrTwoFactorAlreadyEnabled
		}
		if err := s.secondFactor.ReplaceRecoveryCodes(ctx, manage, u.ID, hashes); err != nil {
			return err
		}
		return s.record(ctx, manage, EventTwoFactorEnabled, u)
	}); err != nil {
		return nil, err
	}
	return codes, nil
}

// DisableTwoFactor removes the second factor from the account: the secret, the
// recovery codes, and the memory of which time steps were spent.
//
// All of it, in one transaction, because a half-removed second factor is worse
// than either state. Recovery codes left behind are a factor that reads as off
// and is still open at the door; a secret left behind is one that reads as off
// and starts working again the moment somebody sets the confirmation.
//
// It proves nothing beyond the policy, and a screen that calls it should have
// asked for the password first -- for the reason the reset screen asks: the
// person in front of it may be holding a stolen session, and this is the one
// button that hands them the account.
func (s *Service) DisableTwoFactor(ctx context.Context, actor security.Subject) error {
	manage, err := security.Authorize(ctx, s.twoFactorPolicy, actor, ActionUserTwoFactorManage,
		TwoFactor{UserID: actor.ID, TenantID: actor.Tenant})
	if err != nil {
		return err
	}
	u, err := s.selfForTwoFactor(ctx, actor)
	if err != nil {
		return err
	}

	if err := data.Transaction(ctx, s.repo.db, func(ctx context.Context) error {
		if err := s.secondFactor.Disable(ctx, manage, u.ID); err != nil {
			return err
		}
		return s.record(ctx, manage, EventTwoFactorDisabled, u)
	}); err != nil {
		return err
	}
	// Logged for the reason a replaced password is: it is a security control
	// being taken off, and "when did this account stop having one" is asked
	// afterwards, by somebody who has no request left to read.
	observability.Log(ctx).Warn("second factor disabled", "user", u)
	return nil
}

// RegenerateRecoveryCodes issues a fresh set and invalidates the previous one.
//
// Invalidating is the point rather than a consequence. The reason to ask for
// new codes is that the old ones are on a sheet of paper somebody else may have
// seen, and a set that were merely added beside them would leave that sheet
// able to open the account.
//
// It refuses an account whose second factor is not on, including one whose
// enrolment was begun and never confirmed: recovery codes are the way past a
// factor, and there is nothing yet to get past.
func (s *Service) RegenerateRecoveryCodes(ctx context.Context, actor security.Subject) ([]string, error) {
	candidate := TwoFactor{UserID: actor.ID, TenantID: actor.Tenant}
	read, err := security.Authorize(ctx, s.twoFactorPolicy, actor, ActionUserTwoFactorRead, candidate)
	if err != nil {
		return nil, err
	}
	manage, err := security.Authorize(ctx, s.twoFactorPolicy, actor, ActionUserTwoFactorManage, candidate)
	if err != nil {
		return nil, err
	}
	u, err := s.selfForTwoFactor(ctx, actor)
	if err != nil {
		return nil, err
	}

	enrolment, err := s.secondFactor.Find(ctx, read, u.ID)
	if err != nil {
		return nil, err
	}
	if !enrolment.Enabled() {
		return nil, ErrTwoFactorNotEnrolled
	}

	codes, hashes, err := newRecoveryCodes()
	if err != nil {
		return nil, err
	}
	if err := data.Transaction(ctx, s.repo.db, func(ctx context.Context) error {
		if err := s.secondFactor.ReplaceRecoveryCodes(ctx, manage, u.ID, hashes); err != nil {
			return err
		}
		return s.record(ctx, manage, EventRecoveryCodesRegenerated, u)
	}); err != nil {
		return nil, err
	}
	return codes, nil
}

// TwoFactorRequired reports whether signing in to this account has to be
// challenged for a code.
//
// It answers false for an account that never enrolled and for one whose
// enrolment was begun and never confirmed, and those are the same answer for
// the same reason: a secret nobody has scanned cannot be produced by anybody,
// so a challenge on it is a locked door with no key cut.
//
// It is on the sign-in path, where there is no subject yet, so the read is
// authorized by a system grant -- the same break, and the same auditability, as
// the lookup by address in [Service.Authenticate].
func (s *Service) TwoFactorRequired(ctx context.Context, tenant, userID string) (bool, error) {
	enrolment, err := s.secondFactor.Find(ctx, security.SystemGrant(ActionUserTwoFactorRead, tenant), userID)
	if errors.Is(err, ErrTwoFactorNotEnrolled) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return enrolment.Enabled(), nil
}

// VerifyTwoFactorCode checks the code an authenticator produced, and spends the
// time step it belongs to.
//
// Spending the step is what makes a second factor a second factor. The code is
// correct for the whole of its step, so without this a code read over somebody's
// shoulder -- or captured by whatever put the phishing page in front of them --
// works for as long as it is still on the screen. It is refused on the second
// use with an error that unwraps to [ErrInvalidTwoFactorCode] and can be told
// apart in a log, because a replay is somebody else's attempt and not a typo.
//
// It refuses an enrolment that was never confirmed rather than checking against
// it, so a secret that exists and gates nothing cannot start gating here.
//
// Like [Service.TwoFactorRequired] it runs before there is a subject, so both
// the read and the write are authorized by a system grant.
func (s *Service) VerifyTwoFactorCode(ctx context.Context, tenant, userID, code string) error {
	enrolment, err := s.secondFactor.Find(ctx, security.SystemGrant(ActionUserTwoFactorRead, tenant), userID)
	if err != nil {
		return err
	}
	if !enrolment.Enabled() {
		return ErrTwoFactorNotEnrolled
	}
	secret, err := enrolment.SecretBytes()
	if err != nil {
		return err
	}

	manage := security.SystemGrant(ActionUserTwoFactorManage, tenant)
	guard := twoFactorReplayGuard{repo: s.secondFactor, grant: manage}
	return twofactor.Authenticator{Guard: guard}.Verify(ctx, userID, secret, code)
}

// ConsumeRecoveryCode spends one of the account's recovery codes, and it spends
// it once.
//
// It is the way in when the phone is gone, and it is the only one: nothing here
// sends a code to an address, because an address is not a second factor. A code
// that is not this account's, or is one that has already been spent, comes back
// as [ErrInvalidRecoveryCode] -- one answer, so that the screen cannot be read
// as telling somebody which of the two it was.
//
// # Why the comparison is inside the transaction
//
// [Service.SetPassword] computes its hash before opening a transaction, and the
// rule it states is not broken here, because it is a rule about holding a row
// lock. What happens inside this transaction is a read of the account's unspent
// codes and a password hash comparison against each. Neither locks a row: the
// lock is taken by the single conditional statement at the end, which is also
// where "exactly once" is decided. What the transaction does hold across the
// comparison is a connection, and that is the price of the event and the spend
// committing together -- a code spent with no record of it, or a record of a
// code that was never spent, is worse than a connection held.
func (s *Service) ConsumeRecoveryCode(ctx context.Context, tenant, userID, code string) error {
	read := security.SystemGrant(ActionUserTwoFactorRead, tenant)
	enrolment, err := s.secondFactor.Find(ctx, read, userID)
	if err != nil {
		return err
	}
	if !enrolment.Enabled() {
		return ErrTwoFactorNotEnrolled
	}

	u, err := s.repo.Find(ctx, security.SystemGrant(ActionUserView, tenant), userID)
	if err != nil {
		return err
	}

	manage := security.SystemGrant(ActionUserTwoFactorManage, tenant)
	store := twoFactorRecoveryStore{repo: s.secondFactor, grant: manage}
	if err := data.Transaction(ctx, s.repo.db, func(ctx context.Context) error {
		spent, err := store.Consume(ctx, u.ID, code)
		if err != nil {
			return err
		}
		if !spent {
			return ErrInvalidRecoveryCode
		}
		return s.record(ctx, manage, EventRecoveryCodeUsed, u)
	}); err != nil {
		return err
	}
	// Logged as well as published, because this is the moment the second factor
	// was got past without the device it was bought to require. The event is for
	// whatever has to act now; this line is for the person reading back through
	// an incident later.
	observability.Log(ctx).Warn("recovery code used", "user", u)
	return nil
}

// selfForTwoFactor reads the account the actor is acting on, which is their own.
//
// The read is authorized rather than assumed: the policy answers whether this
// subject may see this user, and the grant it issues is what the repository
// checks. The account is needed for two things the enrolment cannot supply --
// the address the authenticator shows, and the payload of the event.
func (s *Service) selfForTwoFactor(ctx context.Context, actor security.Subject) (User, error) {
	view, err := security.Authorize(ctx, s.policy, actor, ActionUserView,
		User{ID: actor.ID, TenantID: actor.Tenant})
	if err != nil {
		return User{}, err
	}
	return s.repo.Find(ctx, view, actor.ID)
}

// newRecoveryCodes returns a fresh set in the form a person is shown, and the
// password hashes of the same set in the same order.
//
// The plain codes are returned because they are shown once and never again; the
// hashes are what is stored. They are generated in canonical form, so the
// string hashed here is the string a form produces later -- and what is hashed
// is recoveryCodeSecret of it, on both sides, for the reason written there.
func newRecoveryCodes() (codes, hashes []string, err error) {
	codes, err = twofactor.GenerateRecoveryCodes(twofactor.DefaultRecoveryCodes)
	if err != nil {
		return nil, nil, err
	}
	hashes = make([]string, 0, len(codes))
	for _, code := range codes {
		hash, err := security.HashPassword(recoveryCodeSecret(twofactor.NormalizeCode(code)))
		if err != nil {
			return nil, nil, fmt.Errorf("auth: hashing recovery code: %w", err)
		}
		hashes = append(hashes, hash)
	}
	return codes, hashes, nil
}
