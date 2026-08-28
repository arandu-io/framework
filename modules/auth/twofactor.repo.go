package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"
	twofactor "github.com/arandu-io/hesape/2fa"
)

// ErrTwoFactorNotEnrolled is an account with no second factor where one was
// required: nothing was ever enrolled, or it was disabled.
var ErrTwoFactorNotEnrolled = errors.New("auth: this account has no second factor")

// ErrTwoFactorAlreadyEnabled is an enrolment asked for on an account that
// already has a confirmed one.
//
// It exists so that starting the flow again cannot quietly replace a working
// factor with an unconfirmed secret. Between the replacement and the
// confirmation the account would be protected by the password alone, and
// nothing on the screen would say so.
var ErrTwoFactorAlreadyEnabled = errors.New("auth: the second factor is already enabled for this account")

// ErrInvalidTwoFactorCode is the single answer to a code that does not
// authenticate, whichever kind of code it was.
//
// It is the error the twofactor package raises rather than a second one beside
// it, so a caller that already compares against one name does not have to learn
// another -- and so that a wrong code, a replayed code and a spent recovery code
// all come back as the same sentence on the screen.
var ErrInvalidTwoFactorCode = twofactor.ErrInvalidCode

// ErrInvalidRecoveryCode is a recovery code that is not one of this account's,
// or is one that has already been spent.
//
// It unwraps to [ErrInvalidTwoFactorCode], because the person at the keyboard is
// told the same thing either way and telling them more is telling whoever is
// guessing more. A log can still tell the two apart.
var ErrInvalidRecoveryCode = fmt.Errorf("%w: it is not one of this account's recovery codes, or it has already been used", twofactor.ErrInvalidCode)

// The tables this file owns.
const (
	twoFactorTable    = "user_two_factor"
	recoveryCodeTable = "user_recovery_codes"
)

// twoFactorColumns is the projection every read of the enrolment shares.
const twoFactorColumns = `user_id, tenant_id, secret, confirmed_at, last_used_step, created_at`

// twoFactorRepo is the only door to the second factor's two tables.
//
// It is unexported, and built by [NewService] from the handle the user
// repository already holds, for the reason the outbox is: there is nothing for
// an application to decide here. A constructor parameter is something a wiring
// file can pass nil for, and a store that can be wired off is one that is wired
// off in the application that most needed it.
//
// Every method starts with g.Check, and every statement is scoped by
// data.Tenant(g). The SQL uses "?" placeholders and types every supported
// database shares, so the same statements run on SQLite and on PostgreSQL.
type twoFactorRepo struct {
	db *data.DB
}

// newTwoFactorRepo returns the store over an instrumented handle.
func newTwoFactorRepo(db *data.DB) *twoFactorRepo { return &twoFactorRepo{db: db} }

// Find returns one account's enrolment, scoped to the grant's tenant.
//
// A missing row is [ErrTwoFactorNotEnrolled] rather than a zero value, so a
// caller cannot read "no enrolment" as "an enrolment that is switched off" --
// the two differ by nothing in the struct and by everything on the screen.
func (r *twoFactorRepo) Find(ctx context.Context, g security.Grant, userID string) (TwoFactor, error) {
	if err := g.Check(ActionUserTwoFactorRead); err != nil {
		return TwoFactor{}, err
	}
	row := r.db.QueryRowContext(ctx,
		`SELECT `+twoFactorColumns+` FROM `+twoFactorTable+` WHERE user_id = ? AND tenant_id = ?`,
		userID, data.Tenant(g))
	return scanTwoFactor(row)
}

// Enrol stores a fresh, unconfirmed secret for one account.
//
// It refuses an account that already has a confirmed enrolment, and it refuses
// it in the database rather than after a read: the DELETE names
// `confirmed_at IS NULL`, so a confirmed row survives it and the INSERT that
// follows collides with the primary key. Two requests starting the flow at the
// same instant therefore cannot both succeed, and neither can a request that
// races a confirmation -- which a read-then-write would let through, replacing a
// working factor with a secret nobody has scanned.
//
// It must run inside data.Transaction: on its own the two statements are a
// window in which the account has no enrolment at all.
func (r *twoFactorRepo) Enrol(ctx context.Context, g security.Grant, t TwoFactor) (TwoFactor, error) {
	if err := g.Check(ActionUserTwoFactorManage); err != nil {
		return TwoFactor{}, err
	}
	if t.Secret == "" {
		return TwoFactor{}, fmt.Errorf("auth: refusing to store an enrolment without a secret")
	}

	tenant := data.Tenant(g)
	if _, err := r.db.ExecContext(ctx,
		`DELETE FROM `+twoFactorTable+` WHERE user_id = ? AND tenant_id = ? AND confirmed_at IS NULL`,
		t.UserID, tenant); err != nil {
		return TwoFactor{}, err
	}

	t.TenantID = tenant
	t.ConfirmedAt = time.Time{}
	t.LastUsedStep = 0
	t.CreatedAt = time.Now().UTC()

	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO `+twoFactorTable+` (`+twoFactorColumns+`) VALUES (?, ?, ?, ?, ?, ?)`,
		t.UserID, t.TenantID, t.Secret, nullTime(t.ConfirmedAt), int64(t.LastUsedStep), t.CreatedAt); err != nil {
		if isUniqueViolation(err) {
			return TwoFactor{}, ErrTwoFactorAlreadyEnabled
		}
		return TwoFactor{}, err
	}
	return t, nil
}

// Confirm stamps the enrolment as confirmed, and reports whether this call is
// what stamped it.
//
// It is a single conditional statement rather than a read followed by a write,
// and it is [UserRepo.Confirm]'s shape for [UserRepo.Confirm]'s reason: two
// requests carrying the same first code would otherwise both read an
// unconfirmed row, both write, and both publish the event that says the factor
// is now on. `confirmed_at IS NULL` makes the database the referee, so exactly
// one caller is told it turned the factor on -- and the other is told the truth,
// which is that somebody already had.
func (r *twoFactorRepo) Confirm(ctx context.Context, g security.Grant, userID string, at time.Time) (bool, error) {
	if err := g.Check(ActionUserTwoFactorManage); err != nil {
		return false, err
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE `+twoFactorTable+` SET confirmed_at = ?
		 WHERE user_id = ? AND tenant_id = ? AND confirmed_at IS NULL`,
		at.UTC(), userID, data.Tenant(g))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		// Answering "yes, this call did it" without knowing would publish the
		// event twice; answering "no" would lose it. Every driver this framework
		// targets counts, so a driver that cannot is a wiring mistake and says so.
		return false, fmt.Errorf("auth: the driver cannot report how many rows the confirmation changed: %w", err)
	}
	return n > 0, nil
}

// Disable removes the enrolment and every recovery code that belonged to it.
//
// Both, in one call, because a caller that could remove one and not the other
// would eventually be a caller that did: recovery codes outliving the secret
// are a second factor that is off on the screen and still open at the door.
// Removing the enrolment row takes the replay memory with it, which is correct
// -- there is no code left that it could refuse.
//
// It must run inside data.Transaction, and a missing enrolment is
// [ErrTwoFactorNotEnrolled] rather than silence: "turned off" reported for an
// account that never had one is an answer nobody can act on.
func (r *twoFactorRepo) Disable(ctx context.Context, g security.Grant, userID string) error {
	if err := g.Check(ActionUserTwoFactorManage); err != nil {
		return err
	}
	tenant := data.Tenant(g)
	if err := r.deleteRecoveryCodes(ctx, tenant, userID); err != nil {
		return err
	}

	res, err := r.db.ExecContext(ctx,
		`DELETE FROM `+twoFactorTable+` WHERE user_id = ? AND tenant_id = ?`, userID, tenant)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrTwoFactorNotEnrolled
	}
	return nil
}

// SpendStep records step as used by the account and reports whether it was
// still free.
//
// The column is a high-water mark and not a list, and the comparison is `<`, so
// a step below the highest one already spent is refused as well. That is
// stricter than the contract asks and deliberately so: the accepted window is
// wider than one step, and a code from the step before the last accepted one is
// a code somebody is presenting for the second time, whatever the row
// remembers. The alternative -- a row per spent step -- is a table that grows
// for as long as people sign in and needs a sweep nobody would own.
//
// A statement that changes no row answers false, and there are two ways to reach
// that: the step was already spent, or the enrolment was disabled while this
// verification was in flight. Both refuse, which is the direction that cannot
// let a code through, and neither is worth a second query to tell apart.
func (r *twoFactorRepo) SpendStep(ctx context.Context, g security.Grant, userID string, step uint64) (bool, error) {
	if err := g.Check(ActionUserTwoFactorManage); err != nil {
		return false, err
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE `+twoFactorTable+` SET last_used_step = ?
		 WHERE user_id = ? AND tenant_id = ? AND last_used_step < ?`,
		int64(step), userID, data.Tenant(g), int64(step))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		// A guard that cannot tell whether it burned the step must refuse, and
		// the contract it implements says so: there is no path on which storage
		// failing to answer lets a code through.
		return false, fmt.Errorf("auth: the driver cannot report whether the time step was already spent: %w", err)
	}
	return n > 0, nil
}

// ReplaceRecoveryCodes stores a fresh set of hashed codes, and drops whatever
// set was there.
//
// The drop is what makes reissuing mean anything. A set that is added beside
// the previous one leaves a sheet of paper somebody threw away still able to
// open the account, which is the one thing reissuing is asked for.
//
// It must run inside data.Transaction: between the delete and the last insert
// the account has fewer codes than it was told it has, and a failure in the
// middle would leave it there.
func (r *twoFactorRepo) ReplaceRecoveryCodes(ctx context.Context, g security.Grant, userID string, hashes []string) error {
	if err := g.Check(ActionUserTwoFactorManage); err != nil {
		return err
	}
	tenant := data.Tenant(g)
	if err := r.deleteRecoveryCodes(ctx, tenant, userID); err != nil {
		return err
	}

	now := time.Now().UTC()
	for _, hash := range hashes {
		if hash == "" {
			return fmt.Errorf("auth: refusing to store a recovery code without a hash")
		}
		id, err := NewID()
		if err != nil {
			return err
		}
		if _, err := r.db.ExecContext(ctx,
			`INSERT INTO `+recoveryCodeTable+` (id, tenant_id, user_id, code_hash, used_at, created_at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			id, tenant, userID, hash, nil, now); err != nil {
			return err
		}
	}
	return nil
}

// deleteRecoveryCodes removes every code of one account, spent or not. The
// caller has already checked the grant.
func (r *twoFactorRepo) deleteRecoveryCodes(ctx context.Context, tenant, userID string) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM `+recoveryCodeTable+` WHERE user_id = ? AND tenant_id = ?`, userID, tenant)
	return err
}

// ConsumeRecoveryCode spends one of the account's codes and reports whether the
// code was one of them and still unspent.
//
// It is two statements with a comparison between them, and each half answers a
// different half of the guarantee:
//
//   - the SELECT is scoped to the account, so one person's code cannot open
//     another person's account whatever the code happens to collide with;
//   - the UPDATE names `used_at IS NULL`, so two requests arriving together with
//     the same code both find it and only one changes a row. That is where
//     "exactly once" lives, and it lives in the database rather than in a read
//     the caller did first.
//
// The comparison is a password hash's own verifier, which is constant time. It
// stops at the first match, and that leaks nothing a guesser can use: a wrong
// code always costs the full walk, so the short answer only ever happens to
// somebody who is already being told the code was right.
func (r *twoFactorRepo) ConsumeRecoveryCode(ctx context.Context, g security.Grant, userID, code string) (bool, error) {
	if err := g.Check(ActionUserTwoFactorManage); err != nil {
		return false, err
	}
	normalized := twofactor.NormalizeCode(code)
	if normalized == "" {
		return false, nil
	}
	guess := recoveryCodeSecret(normalized)

	tenant := data.Tenant(g)
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, code_hash FROM `+recoveryCodeTable+`
		 WHERE user_id = ? AND tenant_id = ? AND used_at IS NULL`,
		userID, tenant)
	if err != nil {
		return false, err
	}

	var candidates []struct{ id, hash string }
	for rows.Next() {
		var c struct{ id, hash string }
		if err := rows.Scan(&c.id, &c.hash); err != nil {
			rows.Close()
			return false, err
		}
		candidates = append(candidates, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}

	for _, c := range candidates {
		if err := security.VerifyPassword(guess, c.hash); err != nil {
			continue
		}
		res, err := r.db.ExecContext(ctx,
			`UPDATE `+recoveryCodeTable+` SET used_at = ?
			 WHERE id = ? AND tenant_id = ? AND used_at IS NULL`,
			time.Now().UTC(), c.id, tenant)
		if err != nil {
			return false, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			// The same refusal SpendStep makes: a store that cannot say whether
			// it spent the code must not report that it did.
			return false, fmt.Errorf("auth: the driver cannot report whether the recovery code was already spent: %w", err)
		}
		return n > 0, nil
	}
	return false, nil
}

// recoveryCodePrefix is what a recovery code is hashed behind.
//
// It is domain separation, and it does two jobs. The one it was written for:
// the hash of a recovery code and the hash of a password are the same kind of
// value in the same kind of column, so without a prefix a hash moved from one
// column to the other would authenticate, and a person who reused their
// password as a recovery code would have one value opening both doors.
//
// The one that makes it necessary rather than merely correct: the password
// hasher refuses anything shorter than twelve characters, and a recovery code
// is ten. That rule is written for a secret a person chooses, where length is
// the only defence worth having; it is being applied to ten characters a
// machine drew from a thirty-two letter alphabet, which is fifty bits and not
// comparable. The prefix carries the value past a check that is not about it.
const recoveryCodePrefix = "arandu:two-factor-recovery:"

// recoveryCodeSecret is what is hashed and compared, and it is not the code
// itself. It is applied at issue and at use, and the two must not diverge: a
// prefix added on one side only turns every code into a wrong one.
func recoveryCodeSecret(normalized string) string { return recoveryCodePrefix + normalized }

// twoFactorReplayGuard is the memory of which time steps an account has spent.
//
// The interface it satisfies takes an opaque subject and no grant, which is
// what a component that owns no schema can ask for. The tenant therefore cannot
// come from the argument, and it does not: this struct is built per operation
// around the grant the use case already holds, and every statement under it is
// scoped by that grant's tenant. That is also what makes the interface's "two
// accounts must never share one subject" true here regardless of what ids an
// application generates -- the pair is (tenant, user), not the string.
type twoFactorReplayGuard struct {
	repo  *twoFactorRepo
	grant security.Grant
}

// Spend records step as used by subject.
func (g twoFactorReplayGuard) Spend(ctx context.Context, subject string, step uint64) (bool, error) {
	return g.repo.SpendStep(ctx, g.grant, subject, step)
}

// twoFactorRecoveryStore is where recovery codes live between being issued and
// being spent. It is scoped by a grant for the reason [twoFactorReplayGuard] is.
type twoFactorRecoveryStore struct {
	repo  *twoFactorRepo
	grant security.Grant
}

// Consume spends one of subject's recovery codes.
func (s twoFactorRecoveryStore) Consume(ctx context.Context, subject, code string) (bool, error) {
	return s.repo.ConsumeRecoveryCode(ctx, s.grant, subject, code)
}

// Compile-time proof that the two contracts the second factor needs somebody to
// implement are implemented here, where the schema is.
var (
	_ twofactor.ReplayGuard   = twoFactorReplayGuard{}
	_ twofactor.RecoveryStore = twoFactorRecoveryStore{}
)

// scanTwoFactor reads one enrolment row.
func scanTwoFactor(row rowScanner) (TwoFactor, error) {
	var (
		t         TwoFactor
		confirmed sql.NullTime
		step      int64
	)
	err := row.Scan(&t.UserID, &t.TenantID, &t.Secret, &confirmed, &step, &t.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return TwoFactor{}, ErrTwoFactorNotEnrolled
	}
	if err != nil {
		return TwoFactor{}, err
	}
	// Read through sql.NullTime rather than into the field: a plain scan of a
	// NULL is an error, and the column is NULL for every enrolment that has been
	// begun and not finished, which is the state this whole file is careful
	// about.
	t.ConfirmedAt = confirmed.Time.UTC()
	if step > 0 {
		t.LastUsedStep = uint64(step)
	}
	return t, nil
}
