// Tests of the bridge, and of nothing else.
//
// What each symbol DOES is tested in github.com/arandu-io/hesape/filesystem,
// against the code that now runs; the unit tests that used to live here were
// tests of an implementation this package no longer holds, and keeping a second
// copy of them would be a second place for the behaviour to be described.
//
// What is left to prove is the only thing this package still claims: that the
// old name reaches the new behaviour. That is one assertion per alias -- the
// compiler makes it, so it is written as an assignment -- one round trip for the
// two functions, and one compile-time assertion for each of the two shapes that
// stayed declared, because those are the ones a rename in hesape could quietly
// stop matching.

package unit

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/framework/storage"
	"github.com/arandu-io/hesape/filesystem"
)

const tenant = "11111111-1111-4111-8111-111111111111"

func grant(t string) security.Grant { return security.SystemGrant("file.read", t) }

// TestAliasesAreTheHesapeSymbols is the alias half of this bridge.
//
// Each pair is one value, not two that happen to read alike, so a driver
// returning storage.ErrNotFound satisfies a caller testing for
// filesystem.ErrNotFound and the other way round.
func TestAliasesAreTheHesapeSymbols(t *testing.T) {
	for name, pair := range map[string][2]error{
		"ErrNotFound": {storage.ErrNotFound, filesystem.ErrNotFound},
		"ErrNoTenant": {storage.ErrNoTenant, filesystem.ErrNoTenant},
		"ErrBadKey":   {storage.ErrBadKey, filesystem.ErrBadKey},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s is not the hesape value", name)
		}
	}
}

// TestPathReachesFilesystemKey: Path was renamed to Key on the way to hesape,
// and a rename is where a wrapper can be wired to the wrong function and still
// compile.
func TestPathReachesFilesystemKey(t *testing.T) {
	got, err := storage.Path(grant(tenant), "invoices/2026-08.pdf")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	want, err := filesystem.Key(grant(tenant), "invoices/2026-08.pdf")
	if err != nil {
		t.Fatalf("filesystem.Key: %v", err)
	}
	if got != want {
		t.Fatalf("Path = %q, filesystem.Key = %q", got, want)
	}
}

// TestCleanKeyReachesFilesystemCleanKey is the same assertion for the function
// that kept its name: same name, still a wrapper, still able to point elsewhere.
func TestCleanKeyReachesFilesystemCleanKey(t *testing.T) {
	got, err := storage.CleanKey("a/b/../c.pdf")
	if err != nil {
		t.Fatalf("CleanKey: %v", err)
	}
	want, err := filesystem.CleanKey("a/b/../c.pdf")
	if err != nil {
		t.Fatalf("filesystem.CleanKey: %v", err)
	}
	if got != want {
		t.Fatalf("CleanKey = %q, filesystem.CleanKey = %q", got, want)
	}
}

// TestThePathIsStillPrefixedByTheTenant checks the tenant prefix through the old
// name. The enforcement moved, and this is what proves the move did not lose it:
// the prefix comes from the Grant and there is no key that reaches another
// tenant.
func TestThePathIsStillPrefixedByTheTenant(t *testing.T) {
	got, err := storage.Path(grant(tenant), "invoices/2026-08.pdf")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if want := tenant + "/invoices/2026-08.pdf"; got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}

	other := "22222222-2222-4222-8222-222222222222"
	theirs, err := storage.Path(grant(other), "invoice.pdf")
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := storage.Path(grant(tenant), other+"/invoice.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if attempt == theirs {
		t.Fatal("a key naming another tenant reached that tenant's path")
	}
}

// TestTheRefusalsStillReachTheOldNames: a Grant with no tenant, a tenant that is
// itself a path, and a key that escapes are all refused through Path, and the
// error a caller matches on is the aliased one.
func TestTheRefusalsStillReachTheOldNames(t *testing.T) {
	if _, err := storage.Path(grant(""), "x.pdf"); !errors.Is(err, storage.ErrNoTenant) {
		t.Fatalf("empty tenant: err = %v, want ErrNoTenant", err)
	}
	if _, err := storage.Path(grant("acme/reports"), "q1.pdf"); !errors.Is(err, storage.ErrNoTenant) {
		t.Fatalf("tenant as a path: err = %v, want ErrNoTenant", err)
	}
	if _, err := storage.Path(grant(tenant), "../other/secret.pdf"); !errors.Is(err, storage.ErrBadKey) {
		t.Fatalf("escaping key: err = %v, want ErrBadKey", err)
	}
	if _, err := storage.CleanKey("../escape"); !errors.Is(err, storage.ErrBadKey) {
		t.Fatalf("CleanKey: err = %v, want ErrBadKey", err)
	}
}

// noopStore is the shape a driver implements, written out here so that a change
// to Store fails in this module rather than in theirs.
type noopStore struct{}

func (noopStore) Put(context.Context, security.Grant, string, io.Reader, string) error {
	return nil
}
func (noopStore) Get(context.Context, security.Grant, string) (storage.File, error) {
	return storage.File{}, storage.ErrNotFound
}
func (noopStore) Delete(context.Context, security.Grant, string) error { return nil }
func (noopStore) List(context.Context, security.Grant, string) ([]string, error) {
	return nil, nil
}
func (noopStore) Exists(context.Context, security.Grant, string) (bool, error) { return false, nil }

var _ storage.Store = noopStore{}

// TestFileIsStillBuiltFlat is the other half of the same guarantee. A driver
// writes this literal, and it is the reason File did not become an alias for
// filesystem.File, whose fields sit inside an embedded Info.
func TestFileIsStillBuiltFlat(t *testing.T) {
	f := storage.File{
		Key:         "invoice.pdf",
		Size:        12,
		ContentType: "application/pdf",
		Body:        io.NopCloser(nil),
	}
	if f.Key != "invoice.pdf" || f.Size != 12 || f.ContentType != "application/pdf" {
		t.Fatalf("File did not keep its fields: %+v", f)
	}
	if !f.ModifiedAt.IsZero() {
		t.Fatal("the zero File should carry a zero ModifiedAt")
	}
}
