package auth

import (
	"encoding/json"
	"log/slog"
	"time"
)

// User is the entity. It has no persistence methods: this is not Active Record.
//
// The Password field holds an argon2id hash and never leaves this type -- see
// MarshalJSON and LogValue below.
type User struct {
	ID       string
	TenantID string

	// Name is what the application shows instead of the address. It is optional
	// -- an application that never asks for one leaves it empty and nothing
	// breaks -- and it is the field a comment thread signs a post with.
	Name string

	Email    string
	Password string
	Roles    []string

	// VerifiedAt is when the address was confirmed, and the zero value means it
	// was not.
	//
	// A time and not a bool, because "when" is the question asked afterwards --
	// by support, by a fraud check, by a report -- and a bool cannot be widened
	// into it later without a second column.
	VerifiedAt time.Time

	CreatedAt time.Time
}

// Verified reports whether the address was confirmed.
//
// A method rather than the comparison at each call site: `!u.VerifiedAt.IsZero()`
// written in eight places is eight chances to write it without the `!`.
func (u User) Verified() bool { return !u.VerifiedAt.IsZero() }

// MarshalJSON keeps the hash out of any response, log or dump. Without it, a
// single observability.Dump(ctx, "user", u) would publish the hash on the debug
// page.
func (u User) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	}{ID: u.ID, Email: u.Email})
}

// LogValue implements slog.LogValuer, so passing the whole user to a log call
// records the id and nothing else. This is the safe default: it means a careless
// log line cannot leak the hash.
func (u User) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("id", u.ID),
		slog.String("tenant", u.TenantID),
	)
}
