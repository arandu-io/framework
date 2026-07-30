package security_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/arandu-io/framework/security"
)

const validPassword = "correct horse battery"

func TestHashAndVerify(t *testing.T) {
	hash, err := security.HashPassword(validPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if strings.Contains(hash, validPassword) {
		t.Fatal("the hash must not contain the password")
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$") {
		t.Fatalf("hash is not in PHC format: %s", hash)
	}
	if err := security.VerifyPassword(validPassword, hash); err != nil {
		t.Fatalf("VerifyPassword on the right password: %v", err)
	}
}

func TestVerifyRejectsTheWrongPassword(t *testing.T) {
	hash, err := security.HashPassword(validPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	err = security.VerifyPassword(validPassword+"x", hash)
	if !errors.Is(err, security.ErrInvalidPassword) {
		t.Fatalf("error = %v, want ErrInvalidPassword", err)
	}
}

// TestHashIsSalted proves the salt is per hash: two hashes of the same password
// must differ, or the database becomes a rainbow table lookup.
func TestHashIsSalted(t *testing.T) {
	a, err := security.HashPassword(validPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	b, err := security.HashPassword(validPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	if a == b {
		t.Fatal("two hashes of the same password are identical: the salt is not random")
	}
}

func TestHashRejectsShortPassword(t *testing.T) {
	_, err := security.HashPassword(strings.Repeat("a", security.MinPasswordLen-1))
	if err == nil {
		t.Fatal("HashPassword accepted a password below the minimum length")
	}
}

func TestVerifyRejectsMalformedHash(t *testing.T) {
	cases := map[string]string{
		"empty":         "",
		"not phc":       "plaintext",
		"other scheme":  "$bcrypt$v=19$m=1,t=1,p=1$c2FsdA$a2V5",
		"missing parts": "$argon2id$v=19$m=65536,t=3,p=4",
		"bad params":    "$argon2id$v=19$m=x,t=y,p=z$c2FsdA$a2V5",
		"bad base64":    "$argon2id$v=19$m=65536,t=3,p=4$!!!$!!!",
	}
	for name, encoded := range cases {
		t.Run(name, func(t *testing.T) {
			if err := security.VerifyPassword(validPassword, encoded); err == nil {
				t.Fatal("VerifyPassword accepted a malformed hash")
			}
		})
	}
}

func TestNeedsRehash(t *testing.T) {
	current, err := security.HashPassword(validPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if security.NeedsRehash(current) {
		t.Fatal("a hash written with the current parameters must not need a rehash")
	}

	legacy := "$argon2id$v=19$m=4096,t=1,p=1$c2FsdHNhbHRzYWx0c2E$a2V5"
	if !security.NeedsRehash(legacy) {
		t.Fatal("a hash with weaker parameters must be flagged for rehash")
	}
}
