package auth

import (
	"encoding/json"
	"log/slog"
	"time"

	"github.com/arandu-io/hesape/otp"
)

// redactedSecret stands in for key material wherever a type in this file is
// serialized.
//
// A marker rather than an empty string, so a dump still answers whether the
// value was there. "It was filled in and you may not see it" and "it was never
// set" are different answers, and only one of them is a reason to look further.
const redactedSecret = "[redacted]"

// TwoFactor is one account's enrolment in the second factor.
//
// It is a row of its own rather than columns on [User], and the two fields
// below are the reason. Secret is the factor itself: whoever reads it produces
// that account's codes for as long as the enrolment lasts. LastUsedStep is
// written on every accepted code. Neither belongs on the row that every
// listing, every lookup by address and every sign-in already reads.
//
// The zero value is an account with no second factor, which is what a lookup
// that found nothing returns.
type TwoFactor struct {
	UserID   string
	TenantID string

	// Secret is the shared secret in the text form an authenticator reads. It
	// is what the provisioning URI carries, and what a person types when the
	// camera will not focus.
	//
	// This is key material and it is the whole of the factor. It does not leave
	// this type through JSON, through a log or through a formatted string --
	// see [TwoFactor.MarshalJSON], [TwoFactor.LogValue] and [TwoFactor.String].
	Secret string

	// ConfirmedAt is when the first code was verified, and the zero value means
	// it never was.
	//
	// Enrolment writes the secret and leaves this empty. Until a code proves
	// that an authenticator holds the same secret, there is nothing to gate a
	// sign-in with: a secret nobody managed to scan would lock the account out
	// of itself, and the person would have no way to tell why.
	ConfirmedAt time.Time

	// LastUsedStep is the highest time step a code has been accepted for, and
	// zero means none has.
	//
	// It is the memory the algorithm does not have. A correct code stays
	// correct for the whole of its time step, so without this a code read over
	// somebody's shoulder works for as long as it is still on the screen.
	//
	// Zero is safe to mean "none" because step zero is the Unix epoch: no code
	// anybody types belongs to it.
	LastUsedStep uint64

	CreatedAt time.Time
}

// Enabled reports whether the second factor gates this account.
//
// It reads the confirmation and not the secret, and that is the whole of the
// rule: an enrolment that was started and never finished has a secret, and
// gates nothing.
func (t TwoFactor) Enabled() bool { return !t.ConfirmedAt.IsZero() }

// SecretBytes decodes [TwoFactor.Secret] into the bytes the algorithm takes.
func (t TwoFactor) SecretBytes() ([]byte, error) { return otp.DecodeSecret(t.Secret) }

// marker reports the secret as a dump may see it: the marker when there is one,
// the empty string when there is not.
func (t TwoFactor) marker() string {
	if t.Secret == "" {
		return ""
	}
	return redactedSecret
}

// MarshalJSON names the fields that may leave, and the secret is not one of
// them.
//
// Without it a single observability.Dump(ctx, "two_factor", t) publishes the
// second factor on the debug page, and whoever reads it there produces that
// account's codes for as long as the enrolment lasts.
//
// It is an allowlist and not a denylist: a field added to the struct later does
// not appear until it is named here, which is the direction that cannot leak by
// accident. The secret is named, carrying the marker instead of the value, so a
// dump still answers whether the enrolment had one.
func (t TwoFactor) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		UserID   string `json:"user_id"`
		TenantID string `json:"tenant_id"`
		Enabled  bool   `json:"enabled"`
		Secret   string `json:"secret"`
	}{UserID: t.UserID, TenantID: t.TenantID, Enabled: t.Enabled(), Secret: t.marker()})
}

// LogValue implements slog.LogValuer, so passing the whole enrolment to a log
// call records who it belongs to and whether it is on.
//
// Shorter than [TwoFactor.MarshalJSON] on purpose, and the secret is not in it
// at all -- not even as a marker. A log line is shipped to an aggregator and
// kept; the debug page is one request, on one laptop, in development.
func (t TwoFactor) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("user", t.UserID),
		slog.String("tenant", t.TenantID),
		slog.Bool("enabled", t.Enabled()),
	)
}

// String keeps the secret out of a formatted string.
//
// [TwoFactor.MarshalJSON] and [TwoFactor.LogValue] close the two doors a
// serializer goes through; this closes the one a person opens by hand, which is
// fmt with %v on a struct that has no String method. The value it holds is the
// factor rather than a hash of it, so the cheap door is worth closing too.
func (t TwoFactor) String() string {
	if t.UserID == "" {
		return "two factor: none"
	}
	if t.Enabled() {
		return "two factor: enabled for " + t.UserID
	}
	return "two factor: awaiting confirmation for " + t.UserID
}

// TwoFactorEnrolment is what the enrolment screen shows, once: the key a camera
// reads and the key a person types.
//
// Both fields carry the same secret in different clothes, so there is nothing
// in this struct that is safe to serialize. It exists as a type rather than as
// two returned strings so that the three methods below can refuse to print it.
type TwoFactorEnrolment struct {
	// SecretKey is the secret in the text form a person retypes when the camera
	// will not focus.
	SecretKey string

	// URI is the otpauth:// provisioning URI, which is what a QR code for this
	// enrolment encodes. It carries the same secret as [TwoFactorEnrolment.SecretKey].
	URI string
}

// MarshalJSON refuses to publish either field.
//
// The allowlist of this type is empty, and that is not an oversight: every
// field it has is the shared secret. What is left is the marker, so a dump says
// an enrolment was in flight without saying which one.
func (e TwoFactorEnrolment) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		SecretKey string `json:"secret_key"`
		URI       string `json:"uri"`
	}{SecretKey: redactedSecret, URI: redactedSecret})
}

// LogValue implements slog.LogValuer, and records that there was an enrolment
// and nothing about it.
func (e TwoFactorEnrolment) LogValue() slog.Value {
	return slog.GroupValue(slog.String("two_factor_enrolment", redactedSecret))
}

// String keeps both fields out of a formatted string, for the reason
// [TwoFactor.String] gives.
func (e TwoFactorEnrolment) String() string { return "two factor enrolment: " + redactedSecret }
