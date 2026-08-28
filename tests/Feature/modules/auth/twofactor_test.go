package feature

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/framework/modules/auth"
	"github.com/arandu-io/framework/security"
	twofactor "github.com/arandu-io/hesape/2fa"
	"github.com/arandu-io/hesape/otp"
)

// The second factor, from the enrolment screen to the recovery code somebody
// types a year later.
//
// Every test here is written around one property that has to survive somebody
// editing the code without reading it, and the properties are not
// interchangeable: an enrolment that gates a sign-in before it was confirmed
// locks people out, a code that works twice is not a second factor, and a
// recovery code that works twice is a password with extra steps.

const (
	twoFactorTenant = "t1"
	twoFactorIssuer = "Arandu"
)

// twoFactorAccount seeds an account and returns it with the subject that acts
// as it.
func twoFactorAccount(t *testing.T, db *fakeDB, id, email string) (auth.User, security.Subject) {
	t.Helper()
	u := auth.User{ID: id, TenantID: twoFactorTenant, Email: email, Password: "stored-hash"}
	db.seedUser(u)
	return u, auth.SubjectOf(u)
}

// codeFor returns the code the authenticator would be showing for this secret
// at this instant.
func codeFor(t *testing.T, secretKey string) string {
	t.Helper()
	return codeAt(t, secretKey, time.Now())
}

// codeNextStep returns the code the authenticator will show one time step from
// now, which the tolerance window accepts today.
//
// It exists because confirming an enrolment spends the step it was confirmed
// in, so every code drawn in the same thirty seconds is a code that has already
// been used -- correctly, and inconveniently for a test that needs an unspent
// one without waiting half a minute for it. The step after this one is what an
// authenticator on a phone whose clock runs a little fast produces, and the
// tolerance exists for exactly that phone.
func codeNextStep(t *testing.T, secretKey string) string {
	t.Helper()
	return codeAt(t, secretKey, time.Now().Add(otp.DefaultPeriod))
}

func codeAt(t *testing.T, secretKey string, at time.Time) string {
	t.Helper()
	secret, err := otp.DecodeSecret(secretKey)
	if err != nil {
		t.Fatalf("decoding the secret the enrolment screen showed: %v", err)
	}
	code, err := otp.Default().Generate(secret, at)
	if err != nil {
		t.Fatalf("generating the code an authenticator would show: %v", err)
	}
	return code
}

// enableSecondFactor walks an account through the whole enrolment and returns
// the secret the screen showed and the recovery codes the confirmation issued.
//
// It asserts nothing beyond the calls succeeding: the properties of each step
// are what the tests below are for, and a helper that also checked them would be
// checking them in every test whether or not that test could fail on them.
func enableSecondFactor(t *testing.T, svc *auth.Service, actor security.Subject) (secretKey string, codes []string) {
	t.Helper()
	ctx := context.Background()

	enrolment, err := svc.BeginTwoFactorEnrolment(ctx, actor, twoFactorIssuer)
	if err != nil {
		t.Fatalf("beginning the enrolment: %v", err)
	}
	codes, err = svc.ConfirmTwoFactorEnrolment(ctx, actor, codeFor(t, enrolment.SecretKey))
	if err != nil {
		t.Fatalf("confirming the enrolment: %v", err)
	}
	return enrolment.SecretKey, codes
}

// A secret that has been written down and never confirmed gates nothing.
//
// This is the property that decides whether enrolling is safe to offer at all.
// If a written secret counted, then every person whose camera would not focus,
// whose authenticator was on a phone with a flat battery, or who closed the tab
// halfway, would be locked out of their own account by a form they never
// finished -- with no way left to say so, because saying so requires signing in.
func TestAnUnconfirmedEnrolmentDoesNotGateSigningIn(t *testing.T) {
	svc, db := serviceOverFakeDB(t)
	ctx := context.Background()
	u, actor := twoFactorAccount(t, db, "u1", "ana@example.com")

	enrolment, err := svc.BeginTwoFactorEnrolment(ctx, actor, twoFactorIssuer)
	if err != nil {
		t.Fatalf("beginning the enrolment: %v", err)
	}

	// The secret really is stored -- otherwise the two checks below would pass
	// against a method that writes nothing at all, which is the shape of a test
	// that proves the opposite of what it claims.
	if _, stored := db.enrolmentFor(u.ID, u.TenantID); !stored {
		t.Fatal("the enrolment was not stored, so nothing below is a test of what an unconfirmed one does")
	}

	required, err := svc.TwoFactorRequired(ctx, u.TenantID, u.ID)
	if err != nil {
		t.Fatalf("asking whether the second factor is required: %v", err)
	}
	if required {
		t.Error("an enrolment nobody has confirmed was reported as required, which locks out everybody whose scan failed")
	}

	// The stronger half: even a correct code, computed from the very secret the
	// screen showed, must not open the challenge while the enrolment is
	// unconfirmed. A "required" flag that is false while the verification would
	// have said yes is one screen away from being true.
	err = svc.VerifyTwoFactorCode(ctx, u.TenantID, u.ID, codeFor(t, enrolment.SecretKey))
	if !errors.Is(err, auth.ErrTwoFactorNotEnrolled) {
		t.Fatalf("a correct code answered %v against an unconfirmed enrolment, want it refused as not enrolled", err)
	}
}

// Confirming with the first code is what turns the factor on, and it is also
// what hands over the recovery codes.
func TestConfirmingWithTheFirstCodeTurnsTheSecondFactorOn(t *testing.T) {
	svc, db := serviceOverFakeDB(t)
	ctx := context.Background()
	u, actor := twoFactorAccount(t, db, "u1", "ana@example.com")

	_, codes := enableSecondFactor(t, svc, actor)

	if len(codes) != twofactor.DefaultRecoveryCodes {
		t.Errorf("the confirmation issued %d recovery codes, want %d", len(codes), twofactor.DefaultRecoveryCodes)
	}
	required, err := svc.TwoFactorRequired(ctx, u.TenantID, u.ID)
	if err != nil {
		t.Fatalf("asking whether the second factor is required: %v", err)
	}
	if !required {
		t.Fatal("the second factor was confirmed and is not required, so the confirmation turned nothing on")
	}
	if got := db.unusedRecoveryCodes(u.ID, u.TenantID); got != len(codes) {
		t.Errorf("%d recovery codes are spendable and %d were handed to the person: a code somebody wrote down "+
			"and cannot spend is a lockout with a paper trail", got, len(codes))
	}
}

// A code is correct for the whole of its time step, so accepting it twice is
// accepting a replay. What refuses the second use is the step being remembered.
func TestACodeCannotBeUsedTwiceInsideItsTimeStep(t *testing.T) {
	svc, db := serviceOverFakeDB(t)
	ctx := context.Background()
	u, actor := twoFactorAccount(t, db, "u1", "ana@example.com")
	secretKey, _ := enableSecondFactor(t, svc, actor)

	code := codeNextStep(t, secretKey)
	if err := svc.VerifyTwoFactorCode(ctx, u.TenantID, u.ID, code); err != nil {
		t.Fatalf("the first use of a correct code was refused: %v", err)
	}

	err := svc.VerifyTwoFactorCode(ctx, u.TenantID, u.ID, code)
	if !errors.Is(err, auth.ErrInvalidTwoFactorCode) {
		t.Fatalf("the same code was accepted a second time inside its step (%v): a code read over somebody's "+
			"shoulder then works for as long as it is still on their screen", err)
	}
	if !errors.Is(err, twofactor.ErrReplayed) {
		t.Errorf("the second use answered %v, want it told apart as a replay: a replay is somebody else's "+
			"attempt and a log that cannot see it cannot report it", err)
	}
}

// The code that confirmed the enrolment is spent by confirming it.
//
// Written separately from the test above, because it fails to a different
// mistake: a confirmation that verified the code without going through the
// guard would leave that code live, and the person reading it off the screen
// during enrolment is not always the person who owns the account.
func TestTheCodeThatConfirmedTheEnrolmentCannotThenSignIn(t *testing.T) {
	svc, db := serviceOverFakeDB(t)
	ctx := context.Background()
	u, actor := twoFactorAccount(t, db, "u1", "ana@example.com")

	enrolment, err := svc.BeginTwoFactorEnrolment(ctx, actor, twoFactorIssuer)
	if err != nil {
		t.Fatalf("beginning the enrolment: %v", err)
	}
	code := codeFor(t, enrolment.SecretKey)
	if _, err := svc.ConfirmTwoFactorEnrolment(ctx, actor, code); err != nil {
		t.Fatalf("confirming the enrolment: %v", err)
	}

	if err := svc.VerifyTwoFactorCode(ctx, u.TenantID, u.ID, code); !errors.Is(err, auth.ErrInvalidTwoFactorCode) {
		t.Fatalf("the code that confirmed the enrolment answered %v when replayed at the challenge, want it "+
			"refused: confirming a factor has to spend the step the same way using it does", err)
	}
}

// A recovery code opens the account once, and the others keep working.
func TestARecoveryCodeIsSpentExactlyOnce(t *testing.T) {
	svc, db := serviceOverFakeDB(t)
	ctx := context.Background()
	u, actor := twoFactorAccount(t, db, "u1", "ana@example.com")
	_, codes := enableSecondFactor(t, svc, actor)

	if err := svc.ConsumeRecoveryCode(ctx, u.TenantID, u.ID, codes[0]); err != nil {
		t.Fatalf("the first use of a recovery code was refused: %v", err)
	}

	err := svc.ConsumeRecoveryCode(ctx, u.TenantID, u.ID, codes[0])
	if !errors.Is(err, auth.ErrInvalidRecoveryCode) {
		t.Fatalf("the same recovery code opened the account a second time (%v): a sheet of paper somebody "+
			"photographed is then a key that never wears out", err)
	}
	if !errors.Is(err, auth.ErrInvalidTwoFactorCode) {
		t.Errorf("a spent recovery code answered %v, want it to read as an invalid code so the screen has one "+
			"sentence for every way of getting it wrong", err)
	}

	// Only the one code was burned. Without this the test above also passes
	// against an implementation that spends every code the account has, which
	// would lock somebody out of their own recovery on the first use.
	if err := svc.ConsumeRecoveryCode(ctx, u.TenantID, u.ID, codes[1]); err != nil {
		t.Fatalf("a second, different recovery code was refused after the first was spent: %v", err)
	}
	if got, want := db.unusedRecoveryCodes(u.ID, u.TenantID), len(codes)-2; got != want {
		t.Errorf("%d recovery codes are left and %d should be", got, want)
	}
}

// A code somebody typed with the spacing it was printed in is the same code.
func TestARecoveryCodeIsAcceptedWithTheSpacingItWasPrintedIn(t *testing.T) {
	svc, db := serviceOverFakeDB(t)
	ctx := context.Background()
	u, actor := twoFactorAccount(t, db, "u1", "ana@example.com")
	_, codes := enableSecondFactor(t, svc, actor)

	typed := strings.ToLower(codes[0][:5]) + "-" + strings.ToLower(codes[0][5:])
	if err := svc.ConsumeRecoveryCode(ctx, u.TenantID, u.ID, typed); err != nil {
		t.Fatalf("a recovery code typed as %q was refused (%v): it is read off paper by somebody who cannot "+
			"try again very many times", typed, err)
	}
}

// Reissuing recovery codes is asked for because the old ones may have been
// seen. Codes that keep working afterwards make the request pointless.
func TestReissuingRecoveryCodesRetiresThePreviousSet(t *testing.T) {
	svc, db := serviceOverFakeDB(t)
	ctx := context.Background()
	u, actor := twoFactorAccount(t, db, "u1", "ana@example.com")
	_, old := enableSecondFactor(t, svc, actor)

	fresh, err := svc.RegenerateRecoveryCodes(ctx, actor)
	if err != nil {
		t.Fatalf("reissuing the recovery codes: %v", err)
	}

	// The count is checked first, and it is the assertion that survives the
	// mutation the one below does not: a set that is appended rather than
	// replaced leaves both sets spendable, and an implementation that also
	// happened to reject the old code by some other route would still be
	// storing sixteen.
	if got := db.unusedRecoveryCodes(u.ID, u.TenantID); got != len(fresh) {
		t.Errorf("%d recovery codes are spendable after reissuing %d: the previous set was added to rather "+
			"than replaced", got, len(fresh))
	}
	if err := svc.ConsumeRecoveryCode(ctx, u.TenantID, u.ID, old[0]); !errors.Is(err, auth.ErrInvalidRecoveryCode) {
		t.Fatalf("a code from the retired set answered %v, want it refused: the reason to ask for new codes is "+
			"that the old ones are on a sheet of paper somebody else may have", err)
	}
	if err := svc.ConsumeRecoveryCode(ctx, u.TenantID, u.ID, fresh[0]); err != nil {
		t.Fatalf("a code from the fresh set was refused: %v", err)
	}
}

// Disabling takes away the secret AND the recovery codes.
//
// The obvious proof -- that nothing works afterwards -- passes against an
// implementation that leaves the codes behind, because the disabled enrolment
// is refused before any code is looked at. So the codes are checked where they
// live, and then again through the one path that would reach them: a second
// enrolment on the same account.
func TestDisablingRemovesTheSecretAndTheRecoveryCodes(t *testing.T) {
	svc, db := serviceOverFakeDB(t)
	ctx := context.Background()
	u, actor := twoFactorAccount(t, db, "u1", "ana@example.com")
	_, codes := enableSecondFactor(t, svc, actor)

	if err := svc.DisableTwoFactor(ctx, actor); err != nil {
		t.Fatalf("disabling the second factor: %v", err)
	}

	if _, stored := db.enrolmentFor(u.ID, u.TenantID); stored {
		t.Error("the secret is still stored after the second factor was disabled")
	}
	if got := db.unusedRecoveryCodes(u.ID, u.TenantID); got != 0 {
		t.Errorf("%d recovery codes survived the second factor being disabled", got)
	}
	if required, err := svc.TwoFactorRequired(ctx, u.TenantID, u.ID); err != nil || required {
		t.Errorf("TwoFactorRequired = %v, %v after disabling", required, err)
	}

	// The behavioural half. Somebody who enrols again gets a new secret and a
	// new set of codes; a code from before must not open the account, and it
	// would if the disable had only unhooked the enrolment.
	_, second := enableSecondFactor(t, svc, actor)
	if err := svc.ConsumeRecoveryCode(ctx, u.TenantID, u.ID, codes[0]); !errors.Is(err, auth.ErrInvalidRecoveryCode) {
		t.Fatalf("a recovery code from before the factor was disabled answered %v on the enrolment that "+
			"replaced it, want it refused", err)
	}
	if err := svc.ConsumeRecoveryCode(ctx, u.TenantID, u.ID, second[0]); err != nil {
		t.Fatalf("a recovery code from the new enrolment was refused: %v", err)
	}
}

// Starting the flow again cannot replace a working second factor.
//
// The window it would open is the whole of the protection: between the
// replacement and a confirmation that may never come, the account is behind the
// password alone, and the screen says the factor is on.
func TestStartingEnrolmentAgainCannotReplaceALiveSecondFactor(t *testing.T) {
	svc, db := serviceOverFakeDB(t)
	ctx := context.Background()
	u, actor := twoFactorAccount(t, db, "u1", "ana@example.com")
	secretKey, _ := enableSecondFactor(t, svc, actor)

	_, err := svc.BeginTwoFactorEnrolment(ctx, actor, twoFactorIssuer)
	if !errors.Is(err, auth.ErrTwoFactorAlreadyEnabled) {
		t.Fatalf("a second enrolment over a confirmed one answered %v, want it refused", err)
	}

	stored, ok := db.enrolmentFor(u.ID, u.TenantID)
	if !ok {
		t.Fatal("the confirmed enrolment was removed by the enrolment that was refused")
	}
	if stored.secret != secretKey {
		t.Error("the confirmed secret was replaced by the enrolment that was refused, which leaves the account " +
			"behind the password alone until somebody confirms a secret they may never scan")
	}
	if required, err := svc.TwoFactorRequired(ctx, u.TenantID, u.ID); err != nil || !required {
		t.Errorf("TwoFactorRequired = %v, %v after a refused enrolment: the factor was switched off by a "+
			"request that failed", required, err)
	}
}

// An abandoned enrolment can be started over, which is the case the refusal
// above must not also catch.
func TestStartingEnrolmentAgainReplacesAnUnconfirmedSecret(t *testing.T) {
	svc, db := serviceOverFakeDB(t)
	ctx := context.Background()
	u, actor := twoFactorAccount(t, db, "u1", "ana@example.com")

	first, err := svc.BeginTwoFactorEnrolment(ctx, actor, twoFactorIssuer)
	if err != nil {
		t.Fatalf("beginning the first enrolment: %v", err)
	}
	second, err := svc.BeginTwoFactorEnrolment(ctx, actor, twoFactorIssuer)
	if err != nil {
		t.Fatalf("somebody who closed the tab and came back was refused: %v", err)
	}
	if first.SecretKey == second.SecretKey {
		t.Fatal("the second enrolment handed back the first secret, so the screen and the phone can disagree " +
			"about which one was scanned")
	}

	// The one that counts is the one the phone has, which is the second.
	if _, err := svc.ConfirmTwoFactorEnrolment(ctx, actor, codeFor(t, first.SecretKey)); err == nil {
		t.Fatal("a code from the abandoned secret confirmed the enrolment")
	}
	if _, err := svc.ConfirmTwoFactorEnrolment(ctx, actor, codeFor(t, second.SecretKey)); err != nil {
		t.Fatalf("a code from the secret that is actually stored was refused: %v", err)
	}
	if stored, ok := db.enrolmentFor(u.ID, u.TenantID); !ok || stored.secret != second.SecretKey {
		t.Error("the stored secret is not the one the second enrolment showed")
	}
}

// Nobody but the owner may reach the enrolment, and an administrator is one of
// the nobodies.
//
// This is the policy asked directly, because the use cases cannot express the
// question: they take no target, so the enrolment they act on is always the
// subject's own. That is a stronger property than the policy and it is tested
// below -- but it is a property of today's signatures, and the signature that
// takes a target is the one somebody adds for a support screen. The policy is
// what still refuses when they do.
func TestOnlyTheOwnerIsAllowedNearTheEnrolment(t *testing.T) {
	ctx := context.Background()
	policy := auth.TwoFactorPolicy{}
	enrolment := auth.TwoFactor{UserID: "u1", TenantID: twoFactorTenant}

	owner := auth.SubjectOf(auth.User{ID: "u1", TenantID: twoFactorTenant})
	refused := map[string]security.Subject{
		"another account in the tenant": auth.SubjectOf(auth.User{ID: "u2", TenantID: twoFactorTenant}),
		// The one worth writing down. Everywhere else in this module the rule is
		// "the owner or an administrator", and here it is not: an administrator
		// who could read the secret would produce that account's codes at will,
		// and one who could take the factor away already holds the password
		// reset -- which together are every step of signing in as somebody else.
		"an administrator": auth.SubjectOf(auth.User{ID: "u3", TenantID: twoFactorTenant,
			Roles: []string{"admin"}}),
		"the same id in another tenant": auth.SubjectOf(auth.User{ID: "u1", TenantID: "t2"}),
		"a guest":                       security.Guest(twoFactorTenant),
	}

	for _, action := range []security.Action{auth.ActionUserTwoFactorRead, auth.ActionUserTwoFactorManage} {
		if err := policy.Can(ctx, owner, action, enrolment); err != nil {
			t.Errorf("the account's owner was refused %s: %v", action, err)
		}
		for name, subject := range refused {
			if err := policy.Can(ctx, subject, action, enrolment); err == nil {
				t.Errorf("%s was allowed %s on somebody else's enrolment", name, action)
			}
		}
	}
}

// The use cases act on the caller's own enrolment and on no other, so a
// neighbour or an administrator pressing every button leaves the owner's second
// factor exactly as it was.
func TestNobodyElsesButtonsReachTheOwnersSecondFactor(t *testing.T) {
	svc, db := serviceOverFakeDB(t)
	ctx := context.Background()
	owner, ownerSubject := twoFactorAccount(t, db, "u1", "ana@example.com")
	secretKey, codes := enableSecondFactor(t, svc, ownerSubject)

	_, neighbour := twoFactorAccount(t, db, "u2", "bea@example.com")
	db.seedUser(auth.User{ID: "u3", TenantID: twoFactorTenant, Email: "adm@example.com",
		Password: "stored-hash", Roles: []string{"admin"}})
	admin := auth.SubjectOf(auth.User{ID: "u3", TenantID: twoFactorTenant,
		Email: "adm@example.com", Roles: []string{"admin"}})

	for name, subject := range map[string]security.Subject{
		"another account in the tenant": neighbour,
		"an administrator":              admin,
	} {
		t.Run(name, func(t *testing.T) {
			// Each of these is refused, and what it is refused with is not the
			// point: the point is the assertion after the loop.
			_ = svc.DisableTwoFactor(ctx, subject)
			_, _ = svc.RegenerateRecoveryCodes(ctx, subject)
			_, _ = svc.ConfirmTwoFactorEnrolment(ctx, subject, codeFor(t, secretKey))
		})
	}

	stored, ok := db.enrolmentFor(owner.ID, owner.TenantID)
	if !ok {
		t.Fatal("somebody else's request removed the owner's enrolment")
	}
	if stored.secret != secretKey {
		t.Error("somebody else's request replaced the owner's secret")
	}
	if got := db.unusedRecoveryCodes(owner.ID, owner.TenantID); got != len(codes) {
		t.Errorf("the owner has %d recovery codes left and had %d before anybody else pressed anything",
			got, len(codes))
	}
	if required, err := svc.TwoFactorRequired(ctx, owner.TenantID, owner.ID); err != nil || !required {
		t.Errorf("TwoFactorRequired = %v, %v: somebody else's request switched the owner's factor off",
			required, err)
	}
}

// The secret is key material, and key material does not leave its type.
//
// Three doors, because a value leaves a Go struct through three: a serializer,
// a log call, and somebody writing %v. The first two are what a debug dump and
// an aggregator go through, and the third is what a person does at three in the
// morning.
func TestTheSecretDoesNotLeaveTheEnrolmentType(t *testing.T) {
	const secret = "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"
	enrolment := auth.TwoFactor{
		UserID: "u1", TenantID: twoFactorTenant, Secret: secret,
		ConfirmedAt: time.Now().UTC(), CreatedAt: time.Now().UTC(),
	}

	encoded, err := json.Marshal(enrolment)
	if err != nil {
		t.Fatalf("marshalling the enrolment: %v", err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Errorf("the secret is in the JSON of the enrolment (%s): one dump of it on the debug page and "+
			"whoever reads it produces that account's codes", encoded)
	}
	// The marshal still has to say something, or a test that the secret is
	// absent would pass against a method returning "null".
	if !strings.Contains(string(encoded), `"user_id":"u1"`) {
		t.Errorf("the JSON of the enrolment names no account: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"enabled":true`) {
		t.Errorf("the JSON of the enrolment does not say whether it is on: %s", encoded)
	}

	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("enrolment", "two_factor", enrolment)
	if strings.Contains(buf.String(), secret) {
		t.Errorf("the secret reached a log line (%s), and a log line is shipped somewhere and kept", buf.String())
	}
	if !strings.Contains(buf.String(), "u1") {
		t.Errorf("the log line names no account, so it says nothing at all: %s", buf.String())
	}

	for _, format := range []string{"%v", "%+v", "%s"} {
		if printed := fmt.Sprintf(format, enrolment); strings.Contains(printed, secret) {
			t.Errorf("the secret is in %s of the enrolment: %s", format, printed)
		}
	}
}

// The same three doors, on the type the enrolment screen is handed. Every field
// it has is the secret in different clothes, so the allowlist is empty.
func TestTheSecretDoesNotLeaveTheEnrolmentScreensType(t *testing.T) {
	svc, db := serviceOverFakeDB(t)
	_, actor := twoFactorAccount(t, db, "u1", "ana@example.com")

	enrolment, err := svc.BeginTwoFactorEnrolment(context.Background(), actor, twoFactorIssuer)
	if err != nil {
		t.Fatalf("beginning the enrolment: %v", err)
	}
	if enrolment.SecretKey == "" || enrolment.URI == "" {
		t.Fatal("the enrolment screen was handed nothing, so nothing below is a test of what it hides")
	}

	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("enrolment", "enrolment", enrolment)
	encoded, err := json.Marshal(enrolment)
	if err != nil {
		t.Fatalf("marshalling the enrolment: %v", err)
	}
	printed := fmt.Sprintf("%v %+v %s", enrolment, enrolment, enrolment)

	for _, leaked := range []string{enrolment.SecretKey, enrolment.URI} {
		for where, text := range map[string]string{"JSON": string(encoded), "a log line": buf.String(), "%v": printed} {
			if strings.Contains(text, leaked) {
				t.Errorf("the enrolment secret reached %s: %s", where, text)
			}
		}
	}
}

// The events say what happened and carry nothing that opens the account.
func TestTheSecondFactorEventsCarryNoSecretAndNoCode(t *testing.T) {
	svc, db := serviceOverFakeDB(t)
	ctx := context.Background()
	u, actor := twoFactorAccount(t, db, "u1", "ana@example.com")

	secretKey, codes := enableSecondFactor(t, svc, actor)
	fresh, err := svc.RegenerateRecoveryCodes(ctx, actor)
	if err != nil {
		t.Fatalf("reissuing the recovery codes: %v", err)
	}
	if err := svc.ConsumeRecoveryCode(ctx, u.TenantID, u.ID, fresh[0]); err != nil {
		t.Fatalf("spending a recovery code: %v", err)
	}
	if err := svc.DisableTwoFactor(ctx, actor); err != nil {
		t.Fatalf("disabling the second factor: %v", err)
	}

	published := map[string]bool{}
	for _, stored := range db.storedEvents() {
		published[stored.Name] = true

		for _, leaked := range append(append([]string{secretKey}, codes...), fresh...) {
			if strings.Contains(stored.Payload, leaked) {
				t.Errorf("the payload of %s carries key material: %s", stored.Name, stored.Payload)
			}
		}
		if !strings.Contains(stored.Payload, u.ID) {
			t.Errorf("the payload of %s does not name the account it happened to: %s", stored.Name, stored.Payload)
		}
	}

	for _, name := range []string{
		auth.EventTwoFactorEnabled,
		auth.EventRecoveryCodesRegenerated,
		auth.EventRecoveryCodeUsed,
		auth.EventTwoFactorDisabled,
	} {
		if !published[name] {
			t.Errorf("%s was never published, so nothing downstream can react to it", name)
		}
	}
	// Enrolment publishes nothing, because there is nothing yet to react to and
	// an event here would have to be named for a state the account is not in.
	if published[auth.EventTwoFactorEnabled] && len(db.storedEvents()) == 0 {
		t.Fatal("no events were stored at all")
	}
}

// A wrong code is refused, and confirming an enrolment that is already on is
// refused as such rather than as a wrong code.
func TestAWrongCodeIsRefusedAndASecondConfirmationSaysWhy(t *testing.T) {
	svc, db := serviceOverFakeDB(t)
	ctx := context.Background()
	u, actor := twoFactorAccount(t, db, "u1", "ana@example.com")
	secretKey, _ := enableSecondFactor(t, svc, actor)

	// A code from a secret that is not this account's. Not a constant like
	// "000000", which is also refused for being wrong and would pass against an
	// implementation that refuses everything.
	other, err := svc.BeginTwoFactorEnrolment(ctx, auth.SubjectOf(
		mustSeed(t, db, "u2", "bea@example.com")), twoFactorIssuer)
	if err != nil {
		t.Fatalf("beginning a second account's enrolment: %v", err)
	}
	if err := svc.VerifyTwoFactorCode(ctx, u.TenantID, u.ID, codeFor(t, other.SecretKey)); !errors.Is(err, auth.ErrInvalidTwoFactorCode) {
		t.Fatalf("a code from another account's secret answered %v on this one, want it refused", err)
	}
	// And the refusal did not spend anything: the account's own next code still
	// works, so guessing cannot burn the steps of the person who owns it.
	if err := svc.VerifyTwoFactorCode(ctx, u.TenantID, u.ID, codeNextStep(t, secretKey)); err != nil {
		t.Fatalf("a correct code was refused after a wrong one was tried: %v", err)
	}

	if _, err := svc.ConfirmTwoFactorEnrolment(ctx, actor, codeNextStep(t, secretKey)); !errors.Is(err, auth.ErrTwoFactorAlreadyEnabled) {
		t.Fatalf("confirming an enrolment that is already on answered %v, want it said so", err)
	}
}

// mustSeed puts an account in the table and returns it.
func mustSeed(t *testing.T, db *fakeDB, id, email string) auth.User {
	t.Helper()
	u, _ := twoFactorAccount(t, db, id, email)
	return u
}

// The three states an enrolment can be printed in, and none of them prints a
// secret. Written out because the zero value and the unconfirmed one are the
// two branches nothing else reaches, and an untaken branch is where a leak
// hides.
func TestAnEnrolmentPrintsItsStateAndNeverItsSecret(t *testing.T) {
	const secret = "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"
	for name, enrolment := range map[string]auth.TwoFactor{
		"none":        {},
		"unconfirmed": {UserID: "u1", TenantID: twoFactorTenant, Secret: secret},
		"enabled": {UserID: "u1", TenantID: twoFactorTenant, Secret: secret,
			ConfirmedAt: time.Now().UTC()},
	} {
		t.Run(name, func(t *testing.T) {
			printed := fmt.Sprintf("%v", enrolment)
			if strings.Contains(printed, secret) {
				t.Errorf("the secret is in %q", printed)
			}
			if printed == "" {
				t.Error("an enrolment prints as nothing, which says less than it should")
			}
			encoded, err := json.Marshal(enrolment)
			if err != nil {
				t.Fatalf("marshalling: %v", err)
			}
			if strings.Contains(string(encoded), secret) {
				t.Errorf("the secret is in the JSON: %s", encoded)
			}
			// An enrolment with no secret says so with an empty marker, rather
			// than claiming one is there and hidden.
			wantMarker := enrolment.Secret != ""
			if strings.Contains(string(encoded), `"secret":"[redacted]"`) != wantMarker {
				t.Errorf("the marker does not match whether there is a secret: %s", encoded)
			}
		})
	}
}

// Reissuing codes for an account whose factor is not on is refused, and an
// enrolment that was begun and never confirmed is one of those accounts.
func TestRecoveryCodesAreNotIssuedForAFactorThatIsNotOn(t *testing.T) {
	svc, db := serviceOverFakeDB(t)
	ctx := context.Background()
	_, actor := twoFactorAccount(t, db, "u1", "ana@example.com")

	if _, err := svc.RegenerateRecoveryCodes(ctx, actor); !errors.Is(err, auth.ErrTwoFactorNotEnrolled) {
		t.Fatalf("reissuing for an account that never enrolled answered %v, want it refused", err)
	}
	if _, err := svc.BeginTwoFactorEnrolment(ctx, actor, twoFactorIssuer); err != nil {
		t.Fatalf("beginning the enrolment: %v", err)
	}
	if _, err := svc.RegenerateRecoveryCodes(ctx, actor); !errors.Is(err, auth.ErrTwoFactorNotEnrolled) {
		t.Fatalf("reissuing against an unconfirmed enrolment answered %v, want it refused: recovery codes are "+
			"the way past a factor, and there is nothing yet to get past", err)
	}
}

// A recovery code of one account does not open another's, whatever it collides
// with. The lookup is scoped to the account, and the tenant comes from the
// grant.
func TestARecoveryCodeDoesNotOpenAnotherAccount(t *testing.T) {
	svc, db := serviceOverFakeDB(t)
	ctx := context.Background()
	_, ana := twoFactorAccount(t, db, "u1", "ana@example.com")
	bea, beaActor := twoFactorAccount(t, db, "u2", "bea@example.com")

	_, anaCodes := enableSecondFactor(t, svc, ana)
	enableSecondFactor(t, svc, beaActor)

	if err := svc.ConsumeRecoveryCode(ctx, bea.TenantID, bea.ID, anaCodes[0]); !errors.Is(err, auth.ErrInvalidRecoveryCode) {
		t.Fatalf("one account's recovery code answered %v on another's: %v", err, anaCodes[0])
	}
	// And it is still unspent where it belongs, so the failed attempt did not
	// burn it on the way past.
	if err := svc.ConsumeRecoveryCode(ctx, twoFactorTenant, "u1", anaCodes[0]); err != nil {
		t.Fatalf("the code was refused on its own account after being tried on another: %v", err)
	}
}
