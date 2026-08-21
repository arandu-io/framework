// Tests of the adapter that carries a hesape/session handler back to this
// package's SessionBackend.
//
// What a distributed handler does to a RESP server is tested where that handler
// lives, against a real one. What is left to prove here is the crossing, and
// the handler underneath is the in-memory one on purpose: it satisfies the same
// interface, so a rename or a swapped argument fails here without a server
// having to exist.

package unit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/session"
)

// backend is the adapter over the handler every deployment has, which is the
// one that needs nothing running.
func backend() security.SessionBackend {
	return security.NewSessionBackend(session.NewArrayHandler[security.Subject]())
}

func subject(tenant, id string) security.Subject {
	return security.Subject{ID: id, Tenant: tenant, Roles: []string{"member"}}
}

// TestASessionSurvivesTheRoundTrip is the whole of the first three renames:
// what Put wrote is what Get reads, and Delete makes it stop being there.
//
// The refusal is asserted and not just the absence, because the caller branches
// on it. A backend that answered some other not-found would send somebody to a
// generic error page instead of back to the sign-in screen, and only in the
// deployment that wired this handler.
func TestASessionSurvivesTheRoundTrip(t *testing.T) {
	b := backend()
	ctx := context.Background()

	want := subject("acme", "u-1")
	if err := b.Put(ctx, "session-1", want, time.Hour); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := b.Get(ctx, "session-1")
	if err != nil {
		t.Fatalf("Get after Put: %v", err)
	}
	if got.ID != want.ID || got.Tenant != want.Tenant {
		t.Fatalf("Get = %+v, want the subject Put was given", got)
	}
	if len(got.Roles) != 1 || got.Roles[0] != "member" {
		t.Errorf("the roles did not survive the round trip: %v", got.Roles)
	}

	if err := b.Delete(ctx, "session-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := b.Get(ctx, "session-1"); !errors.Is(err, security.ErrSessionExpired) {
		t.Fatalf("Get after Delete = %v, want ErrSessionExpired", err)
	}
}

// TestDeleteSubjectReachesOneSubjectAndNoOther is the fourth rename, and the
// half an inattentive adapter loses.
//
// DestroyIndex takes three arguments that are all strings, so a pair swapped on
// the way across still compiles and still deletes something. What tells the
// right wiring from the wrong one is what is left afterwards: the session that
// was to be kept, the other account of the same customer, and the same account
// id in a different customer.
func TestDeleteSubjectReachesOneSubjectAndNoOther(t *testing.T) {
	b := backend()
	ctx := context.Background()

	sessions := map[string]security.Subject{
		"keep-this":    subject("acme", "u-1"),
		"drop-this":    subject("acme", "u-1"),
		"drop-this-2":  subject("acme", "u-1"),
		"other-person": subject("acme", "u-2"),
		"other-tenant": subject("globex", "u-1"),
	}
	for id, sub := range sessions {
		if err := b.Put(ctx, id, sub, time.Hour); err != nil {
			t.Fatalf("Put %s: %v", id, err)
		}
	}

	if err := b.DeleteSubject(ctx, "acme", "u-1", "keep-this"); err != nil {
		t.Fatalf("DeleteSubject: %v", err)
	}

	for _, id := range []string{"keep-this", "other-person", "other-tenant"} {
		if _, err := b.Get(ctx, id); err != nil {
			t.Errorf("%s was signed out and should not have been: %v", id, err)
		}
	}
	for _, id := range []string{"drop-this", "drop-this-2"} {
		if _, err := b.Get(ctx, id); !errors.Is(err, security.ErrSessionExpired) {
			t.Errorf("%s is still signed in: %v", id, err)
		}
	}
}

// TestTheStoreAcceptsTheAdapter is the reason the adapter exists: the type the
// application wires is what NewSessionStore takes.
//
// A compile-time assertion written as code, because the failure it guards
// against is a signature, not a value.
func TestTheStoreAcceptsTheAdapter(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")

	if security.NewSessionStore(key, time.Hour, false, backend()) == nil {
		t.Fatal("NewSessionStore returned nothing")
	}
}
