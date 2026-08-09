package security_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/framework/security"
)

func signer() *security.Signer {
	return security.NewSigner([]byte("an application key long enough to be one"))
}

// TestASignedPayloadComesBackUnchanged is the base case: without it, nothing
// below is testing anything.
func TestASignedPayloadComesBackUnchanged(t *testing.T) {
	s := signer()

	token := s.Sign("verify-email", "user-1|reader@example.com", time.Hour)
	got, err := s.Verify("verify-email", token)
	if err != nil {
		t.Fatalf("a token this application just issued was refused: %v", err)
	}
	if got != "user-1|reader@example.com" {
		t.Errorf("the payload came back as %q", got)
	}
}

// TestATokenDoesNotWorkForAnotherPurpose.
//
// This is the reason the type exists rather than a bare HMAC. A verification
// link and a password reset link are both "a link in an e-mail with an id in
// it", signed by the same key -- and without the purpose in the signature,
// clicking the first one resets the password of the second.
func TestATokenDoesNotWorkForAnotherPurpose(t *testing.T) {
	s := signer()

	token := s.Sign("verify-email", "user-1", time.Hour)
	if _, err := s.Verify("password-reset", token); err == nil {
		t.Fatal("a verification token was accepted as a password reset")
	}
}

// TestAPurposeCannotBorrowTheBodysFirstCharacter.
//
// Length-prefixing the purpose is not decoration: "verify" + "|x.y" and
// "verify|" + "x.y" are the same byte string once concatenated, so without the
// prefix one token verifies under two purposes.
func TestAPurposeCannotBorrowTheBodysFirstCharacter(t *testing.T) {
	s := signer()

	token := s.Sign("verify", "payload", time.Hour)
	if _, err := s.Verify("verif", token); err == nil {
		t.Fatal("a token verified under a purpose it was not issued for")
	}
}

// TestTheExpiryCannotBeMovedByEditingTheURL: the expiry is inside the signature,
// so a link that has run out cannot be extended by anyone holding it.
func TestTheExpiryCannotBeMovedByEditingTheURL(t *testing.T) {
	s := signer()

	token := s.Sign("verify-email", "user-1", -time.Minute)
	parts := strings.Split(token, ".")

	forged := parts[0] + "." + "9999999999" + "." + parts[2]
	if _, err := s.Verify("verify-email", forged); !errors.Is(err, security.ErrSignature) {
		t.Fatalf("an edited expiry was accepted: %v", err)
	}
}

// TestAnExpiredTokenSaysSo.
//
// It is the one failure a person can act on. Told "this link is not valid" they
// look for a bug; told it expired they ask for another one.
func TestAnExpiredTokenSaysSo(t *testing.T) {
	s := signer()

	_, err := s.Verify("verify-email", s.Sign("verify-email", "user-1", -time.Second))
	if !errors.Is(err, security.ErrExpired) {
		t.Fatalf("an expired token reported %v", err)
	}
	if !errors.Is(err, security.ErrSignature) {
		t.Error("ErrExpired does not unwrap to ErrSignature: a caller that checks one has to check both")
	}
}

// TestAnotherKeyDoesNotVerify: two applications sharing a domain do not share
// each other's links.
func TestAnotherKeyDoesNotVerify(t *testing.T) {
	token := signer().Sign("verify-email", "user-1", time.Hour)

	other := security.NewSigner([]byte("a different application key entirely"))
	if _, err := other.Verify("verify-email", token); err == nil {
		t.Fatal("a token signed with another key was accepted")
	}
}

// TestGarbageIsRefusedAndNotPanicked. The token arrives in a URL, so every shape
// of it is attacker-chosen.
func TestGarbageIsRefusedAndNotPanicked(t *testing.T) {
	s := signer()

	for _, token := range []string{"", ".", "..", "a.b.c", "a.b.c.d", "!!.!!.!!",
		strings.Repeat("a", 10000)} {
		if _, err := s.Verify("verify-email", token); err == nil {
			t.Errorf("%q was accepted", token)
		}
	}
}
