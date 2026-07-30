package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters. They are deliberately not configurable through the
// environment: an insecure hash configuration is the most common way to break
// authentication without noticing.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // 64 MiB
	argonThreads = 4
	argonKeyLen  = 32
	saltLen      = 16

	// MinPasswordLen is the shortest password the framework accepts. Length
	// beats composition rules: it is the only parameter that reliably raises
	// the cost of an offline attack.
	MinPasswordLen = 12
)

// ErrInvalidPassword means the password does not match the stored hash.
var ErrInvalidPassword = errors.New("arandu: invalid password")

// HashPassword returns the hash in PHC string format, which is
// self-describing and therefore allows rehashing when parameters change.
func HashPassword(plain string) (string, error) {
	if len([]rune(plain)) < MinPasswordLen {
		return "", fmt.Errorf("arandu: password must be at least %d characters", MinPasswordLen)
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(plain), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword compares in constant time.
func VerifyPassword(plain, encoded string) error {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return fmt.Errorf("arandu: unknown hash format")
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return fmt.Errorf("arandu: unreadable hash parameters: %w", err)
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return fmt.Errorf("arandu: unreadable hash salt: %w", err)
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return fmt.Errorf("arandu: unreadable hash key: %w", err)
	}
	got := argon2.IDKey([]byte(plain), salt, t, m, p, uint32(len(want)))
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrInvalidPassword
	}
	return nil
}

// NeedsRehash reports that the hash was produced with older parameters, so the
// caller should rehash on the next successful login.
func NeedsRehash(encoded string) bool {
	return !strings.Contains(encoded, fmt.Sprintf("m=%d,t=%d,p=%d", argonMemory, argonTime, argonThreads))
}
