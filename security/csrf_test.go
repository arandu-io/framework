package security_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/framework/security"
)

var appKey = []byte("0123456789abcdef0123456789abcdef")

func TestCSRFIssueAndValidate(t *testing.T) {
	c := security.NewCSRF(appKey, time.Hour)

	token, err := c.Issue("session-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if err := c.Validate("session-1", token); err != nil {
		t.Fatalf("Validate on a fresh token: %v", err)
	}
}

// TestCSRFIsBoundToTheSession is the property that makes double-submit safe: a
// token stolen from one session is useless in another.
func TestCSRFIsBoundToTheSession(t *testing.T) {
	c := security.NewCSRF(appKey, time.Hour)

	token, err := c.Issue("session-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if err := c.Validate("session-2", token); !errors.Is(err, security.ErrCSRF) {
		t.Fatalf("error = %v, want ErrCSRF", err)
	}
}

func TestCSRFRejectsAnotherKey(t *testing.T) {
	issuer := security.NewCSRF(appKey, time.Hour)
	other := security.NewCSRF([]byte("ffffffffffffffffffffffffffffffff"), time.Hour)

	token, err := issuer.Issue("session-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if err := other.Validate("session-1", token); !errors.Is(err, security.ErrCSRF) {
		t.Fatalf("error = %v, want ErrCSRF", err)
	}
}

func TestCSRFRejectsExpiredToken(t *testing.T) {
	c := security.NewCSRF(appKey, -time.Second)

	token, err := c.Issue("session-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if err := c.Validate("session-1", token); !errors.Is(err, security.ErrCSRF) {
		t.Fatalf("error = %v, want ErrCSRF", err)
	}
}

// TestCSRFRejectsTamperedExpiry proves the expiry is signed, not just carried:
// otherwise anyone could extend their own token.
func TestCSRFRejectsTamperedExpiry(t *testing.T) {
	c := security.NewCSRF(appKey, time.Hour)

	token, err := c.Issue("session-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d parts, want 3", len(parts))
	}
	tampered := parts[0] + "." + "9999999999" + "." + parts[2]

	if err := c.Validate("session-1", tampered); !errors.Is(err, security.ErrCSRF) {
		t.Fatalf("error = %v, want ErrCSRF", err)
	}
}

func TestCSRFRejectsMalformedToken(t *testing.T) {
	c := security.NewCSRF(appKey, time.Hour)

	for _, token := range []string{"", "a", "a.b", "a.b.c.d"} {
		if err := c.Validate("session-1", token); !errors.Is(err, security.ErrCSRF) {
			t.Fatalf("token %q: error = %v, want ErrCSRF", token, err)
		}
	}
}
