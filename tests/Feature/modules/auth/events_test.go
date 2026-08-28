package feature

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/events"
	"github.com/arandu-io/framework/modules/auth"
	"github.com/arandu-io/framework/security"
)

// tenant is a valid one: security.SystemGrant refuses the empty string, and so
// does the outbox, because a row nothing can scope is a row the relay cannot
// deliver.
const tenant = "acme"

// deliveries is the subscriber: what actually came out the other end.
type deliveries struct {
	mu   sync.Mutex
	list []events.Stored
}

func (d *deliveries) add(e events.Stored) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.list = append(d.list, e)
}

func (d *deliveries) all() []events.Stored {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]events.Stored(nil), d.list...)
}

func (d *deliveries) names() []string {
	var out []string
	for _, e := range d.all() {
		out = append(out, e.Name)
	}
	return out
}

// authWithSubscriber wires the module the way an application does, with a
// subscriber behind the relay.
//
// The relay is drained inline rather than left on its ticker: it is the same
// code path production runs, executed when the test asks. A synchronous mode
// would be a second way to publish, and the second way always leaks out of the
// tests it was written for.
func authWithSubscriber(t *testing.T) (*auth.Service, *fakeDB, *deliveries, func()) {
	t.Helper()

	sqlDB, state := newFakeDB()
	t.Cleanup(func() { _ = sqlDB.Close() })

	db := data.Wrap(sqlDB, data.DialectSQLite)
	service := auth.NewService(auth.NewUserRepo(db), nil, nil)

	got := &deliveries{}
	relay := events.NewRelay(events.NewOutbox(db), events.PublisherFunc(
		func(_ context.Context, e events.Stored) error {
			got.add(e)
			return nil
		}), events.RelayOptions{})

	return service, state, got, func() {
		t.Helper()
		if err := relay.Drain(context.Background()); err != nil {
			t.Fatalf("the relay could not deliver what the module stored: %v", err)
		}
	}
}

// TestEveryAuthMilestoneReachesASubscriber.
//
// The three moments another part of the system has to react to -- an account was
// created, an address became real, a credential changed -- have to leave the
// module. They used to reach observability.Collector.RecordEvent, which is the
// ring buffer behind /_arandu/debug and is nil outside development: a welcome
// mail, a CRM sync and a fraud review had nothing to subscribe to, and the
// method name read like a dispatch.
func TestEveryAuthMilestoneReachesASubscriber(t *testing.T) {
	service, db, got, drain := authWithSubscriber(t)
	ctx := context.Background()

	registered, err := service.Register(ctx, tenant, auth.RegisterRequest{
		Name:                 "Ada Lovelace",
		Email:                "Ada@Example.COM",
		Password:             "a-long-enough-password",
		PasswordConfirmation: "a-long-enough-password",
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	unconfirmed := auth.User{
		ID: "u-1", TenantID: tenant, Name: "Ada", Email: "ada@example.com",
		Password: "an-argon2-hash", CreatedAt: time.Now().UTC(),
	}
	db.seedUser(unconfirmed)

	if _, first, err := service.MarkVerified(ctx, tenant, auth.VerificationPayload(unconfirmed)); err != nil {
		t.Fatalf("MarkVerified: %v", err)
	} else if !first {
		t.Fatal("the click that confirmed the address did not report itself as the one that did")
	}

	if _, err := service.SetPassword(ctx, tenant, unconfirmed.Email, "another-long-password"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}

	drain()

	want := []string{auth.EventUserRegistered, auth.EventEmailVerified, auth.EventPasswordReset}
	if got := got.names(); !equal(got, want) {
		t.Fatalf("the subscriber received %v, and has to receive %v", got, want)
	}

	for _, e := range got.all() {
		if e.TenantID != tenant {
			t.Errorf("%s left the module carrying the tenant %q: the relay cannot deliver what it cannot scope", e.Name, e.TenantID)
		}
		if e.Aggregate != "user" || e.AggregateID == "" {
			t.Errorf("%s does not say what it happened to: aggregate %q, id %q", e.Name, e.Aggregate, e.AggregateID)
		}
		var payload auth.UserEvent
		if err := e.Decode(&payload); err != nil {
			t.Fatalf("a subscriber cannot read the payload of %s: %v", e.Name, err)
		}
		if payload.Email == "" || payload.UserID == "" {
			t.Errorf("%s tells a consumer to go look the account up: %+v", e.Name, payload)
		}
		if strings.Contains(e.Payload, "argon2") || strings.Contains(e.Payload, "$") {
			t.Errorf("%s carries the password hash into the outbox: %s", e.Name, e.Payload)
		}
	}

	if first := got.all()[0]; first.AggregateID != registered.ID {
		t.Errorf("the registration event is about %s, and the account created is %s", first.AggregateID, registered.ID)
	}
}

// TestASecondClickOnTheSameLinkPublishesNothing.
//
// Somebody opens the mail in a second client, or the first tab was closed. The
// address is already confirmed, so nothing happened -- and an event for
// something that did not happen is a welcome mail sent twice, by a consumer that
// is behaving correctly.
func TestASecondClickOnTheSameLinkPublishesNothing(t *testing.T) {
	service, db, got, drain := authWithSubscriber(t)

	confirmed := auth.User{
		ID: "u-2", TenantID: tenant, Email: "grace@example.com",
		Password: "an-argon2-hash", VerifiedAt: time.Now().UTC().Add(-time.Hour),
	}
	db.seedUser(confirmed)

	_, first, err := service.MarkVerified(context.Background(), tenant, auth.VerificationPayload(confirmed))
	if err != nil {
		t.Fatalf("MarkVerified: %v", err)
	}
	if first {
		t.Fatal("an address that was already confirmed reported this click as the one that confirmed it")
	}

	drain()
	if names := got.names(); len(names) != 0 {
		t.Fatalf("the subscriber was told about %v, and nothing happened", names)
	}
}

// TestTwoClicksArrivingTogetherAnnounceOneConfirmation.
//
// The second click was only silent when it arrived after the first one had
// finished. The clicks that actually happen do not: a link in an inbox is opened
// by the person and prefetched by whatever scans their mail within the same
// second, both requests read an unverified row, and both used to write it and
// store auth.email.verified -- so the welcome mail the previous test proves is
// sent once went out twice, from a consumer doing exactly what it was told.
//
// The read cannot decide this and never could. Only the write can, and it does:
// the column is stamped while it is still null, and the event belongs to the
// call the database counted a row for.
func TestTwoClicksArrivingTogetherAnnounceOneConfirmation(t *testing.T) {
	service, db, got, drain := authWithSubscriber(t)

	racing := auth.User{
		ID: "u-5", TenantID: tenant, Email: "hedy@example.com",
		Password: "an-argon2-hash",
	}
	db.seedUser(racing)
	// The other click lands between this call's read and its write.
	db.confirmBehindOurBack()

	u, first, err := service.MarkVerified(context.Background(), tenant, auth.VerificationPayload(racing))
	if err != nil {
		t.Fatalf("MarkVerified: %v", err)
	}
	if first {
		t.Fatal("both clicks were told they were the one that confirmed the address: the welcome mail goes out twice")
	}
	if !u.Verified() {
		t.Fatal("the caller was handed an unverified account right after a successful confirmation")
	}

	drain()
	if names := got.names(); len(names) != 0 {
		t.Fatalf("the subscriber was told about %v by the click that changed nothing", names)
	}
}

// TestAVerificationLinkStopsWorkingWhenTheAddressChanges.
//
// The link proves control of the address it was mailed to. Bound to the id
// alone, it kept confirming the account after the address had been replaced --
// so a link sitting in an inbox somebody no longer owns confirmed an address
// they never proved anything about. Latent only because no method changed an
// address yet, which is exactly how it would have shipped.
func TestAVerificationLinkStopsWorkingWhenTheAddressChanges(t *testing.T) {
	service, db, got, drain := authWithSubscriber(t)
	ctx := context.Background()

	mailed := auth.User{
		ID: "u-3", TenantID: tenant, Email: "ada@old.example",
		Password: "an-argon2-hash",
	}
	link := auth.VerificationPayload(mailed)

	// The address changed after the mail went out. The row now holds the new one.
	moved := mailed
	moved.Email = "ada@new.example"
	db.seedUser(moved)

	if _, _, err := service.MarkVerified(ctx, tenant, link); !errors.Is(err, auth.ErrVerificationAddressChanged) {
		t.Fatalf("error = %v, and a link for an address the account no longer has must be refused", err)
	}

	// A link for the address the account actually has still works, so the
	// binding is a binding and not a wall.
	if _, first, err := service.MarkVerified(ctx, tenant, auth.VerificationPayload(moved)); err != nil || !first {
		t.Fatalf("the link for the current address was refused: first = %v, err = %v", first, err)
	}

	drain()
	if names := got.names(); !equal(names, []string{auth.EventEmailVerified}) {
		t.Fatalf("the subscriber received %v, and only the confirmation that happened may reach it", names)
	}
}

// TestAPayloadCannotBorrowTheAddressesFirstCharacter.
//
// security.Signer writes the purpose with its length in front of it so that
// nothing on either side of the separator can move the boundary between them: a
// purpose of "a|b" with a body of "c" and a purpose of "a" with a body of "b|c"
// are the byte string "a|b|c" either way. The payload inside the token needs
// the same property, or the fix for one token confusion builds another: an id of
// "a" with an address of "b|c" and an id of "a|b" with an address of "c" would
// be the same string, and one account's link would verify another's.
func TestAPayloadCannotBorrowTheAddressesFirstCharacter(t *testing.T) {
	one := auth.VerificationPayload(auth.User{ID: "a", Email: "b|c"})
	two := auth.VerificationPayload(auth.User{ID: "a|b", Email: "c"})

	if one == two {
		t.Fatalf("two different accounts produce the same payload %q: a link for one verifies the other", one)
	}
}

// TestAVerificationTokenIsRefusedForAnotherPurpose.
//
// Both links are "a message with an id in it", signed with the same application
// key, so nothing but the purpose keeps a verification link from being spent as
// a password reset. The payload changed shape; this is what says the separation
// survived it.
func TestAVerificationTokenIsRefusedForAnotherPurpose(t *testing.T) {
	signer := security.NewSigner([]byte("the application key, the same one for both"))
	payload := auth.VerificationPayload(auth.User{ID: "u-4", Email: "ada@example.com"})

	token := signer.Sign("verify-email", payload, time.Hour)

	if _, err := signer.Verify("reset-password", token); !errors.Is(err, security.ErrSignature) {
		t.Fatalf("a verification link was accepted as a password reset: err = %v", err)
	}
	if back, err := signer.Verify("verify-email", token); err != nil || back != payload {
		t.Fatalf("the link no longer works for what it was issued for: %q, %v", back, err)
	}
}

// TestARegistrationThatCannotRecordItsEventDoesNotHappen.
//
// The decision, written down where it can be broken: the event is stored in the
// transaction that created the account, so a failure to store it rolls the
// account back. The alternative -- log it and carry on -- is the account
// existing while nothing downstream ever learns of it, with no row anywhere
// saying something was missed. That is the loss the outbox exists to refuse.
func TestARegistrationThatCannotRecordItsEventDoesNotHappen(t *testing.T) {
	service, db, got, drain := authWithSubscriber(t)
	db.breakTheOutbox()

	_, err := service.Register(context.Background(), tenant, auth.RegisterRequest{
		Name:                 "Ada Lovelace",
		Email:                "ada@example.com",
		Password:             "a-long-enough-password",
		PasswordConfirmation: "a-long-enough-password",
	})
	if err == nil {
		t.Fatal("the account was created and its event was not: nothing downstream will ever know it exists")
	}

	drain()
	if names := got.names(); len(names) != 0 {
		t.Fatalf("the subscriber was told about %v after the write was rolled back", names)
	}
}

// TestTheOutboxRefusesAnEventOutsideATransaction is the guard underneath all of
// the above, asserted from this module because this module is what would break
// it: a service that writes the row and then stores the event, one statement
// later and outside the transaction, still passes every test up there.
func TestTheOutboxRefusesAnEventOutsideATransaction(t *testing.T) {
	sqlDB, _ := newFakeDB()
	t.Cleanup(func() { _ = sqlDB.Close() })

	outbox := events.NewOutbox(data.Wrap(sqlDB, data.DialectSQLite))
	err := outbox.Store(context.Background(), security.SystemGrant(auth.ActionUserUpdate, tenant),
		[]events.Event{{Name: auth.EventPasswordReset, Aggregate: "user", AggregateID: "u-1"}})

	if !errors.Is(err, events.ErrNoTransaction) {
		t.Fatalf("error = %v, and an event stored beside a row that may still roll back is worse than no event", err)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
