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
	ID        string
	TenantID  string
	Email     string
	Password  string
	Roles     []string
	CreatedAt time.Time
}

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
