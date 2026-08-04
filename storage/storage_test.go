package storage_test

import (
	"errors"
	"testing"

	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/framework/storage"
)

const tenant = "11111111-1111-4111-8111-111111111111"

func grant(t string) security.Grant { return security.SystemGrant("file.read", t) }

// TestThePathIsPrefixedByTheTenant is the property every driver inherits by
// calling Path. Without it, tenant isolation would be something each
// implementation has to remember.
func TestThePathIsPrefixedByTheTenant(t *testing.T) {
	got, err := storage.Path(grant(tenant), "invoices/2026-08.pdf")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if want := tenant + "/invoices/2026-08.pdf"; got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

func TestAGrantWithoutATenantIsRefused(t *testing.T) {
	if _, err := storage.Path(grant(""), "x.pdf"); !errors.Is(err, storage.ErrNoTenant) {
		t.Fatalf("err = %v, want ErrNoTenant", err)
	}
}

// TestAKeyCannotEscapeItsTenant is the check that matters. A key is often a
// filename that came from an upload, and "../../../etc/passwd" is what an
// upload form eventually receives.
func TestAKeyCannotEscapeItsTenant(t *testing.T) {
	for _, key := range []string{
		"../other-tenant/secret.pdf",
		"../../etc/passwd",
		"invoices/../../../etc/passwd",
		"..",
		"",
		".",
		"./",
		"a\x00b",
	} {
		if _, err := storage.Path(grant(tenant), key); err == nil {
			t.Errorf("%q was accepted", key)
		}
	}
}

// TestAKeyIsRejectedRatherThanRewritten: a key that had to be rewritten to be
// safe is a key the caller did not mean, and storing it somewhere else quietly
// is worse than an error.
func TestAKeyIsRejectedRatherThanRewritten(t *testing.T) {
	if _, err := storage.CleanKey("../escape"); !errors.Is(err, storage.ErrBadKey) {
		t.Fatalf("err = %v, want ErrBadKey", err)
	}
}

// TestAnAbsoluteKeyIsNotAnEscape: "/etc/passwd" becomes "<tenant>/etc/passwd",
// which is inside the prefix and harmless. Only "../" leaves, and that is what
// the rejection is for -- refusing absolute keys too would reject "/avatar.png"
// from an upload form for no reason anyone could explain.
func TestAnAbsoluteKeyIsNotAnEscape(t *testing.T) {
	got, err := storage.Path(grant(tenant), "/etc/passwd")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if want := tenant + "/etc/passwd"; got != want {
		t.Fatalf("path = %q, want %q -- inside the tenant prefix", got, want)
	}
}

// TestHarmlessKeysStillWork: normalizing has to leave ordinary keys alone, or
// people start working around it.
func TestHarmlessKeysStillWork(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"file.pdf", "file.pdf"},
		{"/file.pdf", "file.pdf"},
		{"a/b/c.pdf", "a/b/c.pdf"},
		{"a//b.pdf", "a/b.pdf"},
		{"a/./b.pdf", "a/b.pdf"},
		{"a/b/../c.pdf", "a/c.pdf"},
	} {
		got, err := storage.CleanKey(c.in)
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("%q became %q, want %q", c.in, got, c.want)
		}
	}
}

// TestOneTenantCannotBuildAnotherTenantsPath: the prefix comes from the Grant
// and never from the key, which is what makes the isolation unbypassable rather
// than conventional.
func TestOneTenantCannotBuildAnotherTenantsPath(t *testing.T) {
	other := "22222222-2222-4222-8222-222222222222"

	mine, err := storage.Path(grant(tenant), "invoice.pdf")
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := storage.Path(grant(other), "invoice.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if mine == theirs {
		t.Fatal("two tenants produced the same path for the same key")
	}

	// And naming the other tenant in the key does not reach it.
	attempt, err := storage.Path(grant(tenant), other+"/invoice.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if attempt == theirs {
		t.Fatal("a key naming another tenant reached that tenant's path")
	}
}
