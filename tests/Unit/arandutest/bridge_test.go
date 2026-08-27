package unit

// What this file is for, and what it deliberately does not do.
//
// It proves that the old name reaches the new behaviour, and nothing else. The
// cookie jar, the CSRF token read off the last page and the comparison each
// assertion makes are tested in github.com/arandu-io/hesape/arandutest, against
// the code that now runs; repeating them here would be a second test of one
// implementation, which is the same mistake as a second implementation.
//
// So: one round trip per envelope, one compile-time assertion per contract.

import (
	"context"
	"net/http"
	"testing"

	"github.com/arandu-io/framework/arandutest"
	"github.com/arandu-io/framework/events"
	"github.com/arandu-io/framework/security"
	hesapetest "github.com/arandu-io/hesape/arandutest"
	"github.com/arandu-io/hesape/auth"
)

// page answers a fixed body, a header and -- when asked -- a redirect, which is
// everything the six forwarded assertions need to be told apart.
func page(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Answered-By", "page")
		if r.URL.Path == "/leave" {
			http.Redirect(w, r, "/arrived", http.StatusSeeOther)
			return
		}
		_, _ = w.Write([]byte("the invoice is paid"))
	})
}

// Each old assertion has to land on exactly one hesape method. A forward wired
// to the wrong one passes every green test and fails to fail, which is the only
// way an assertion helper can be broken without anybody noticing.
func TestTheOldAssertionNamesReachTheRenamedOnes(t *testing.T) {
	client := arandutest.NewClient(t, page(t))

	res := client.Get("/")
	res.OK().Status(http.StatusOK).See("invoice").DontSee("refunded")

	if got := res.Body(); got != "the invoice is paid" {
		t.Errorf("Body does not reach GetContent: %q", got)
	}
	if got := res.Header("X-Answered-By"); got != "page" {
		t.Errorf("Header does not reach the recorded header: %q", got)
	}

	client.Get("/leave").Status(http.StatusSeeOther).RedirectsTo("/arrived")
}

// The envelope must hold one hesape client for the life of the test, not build
// a fresh one per request. A client rebuilt per call has an empty jar, so a
// session never survives a redirect and every feature test after a sign-in is
// silently anonymous -- the failure the jar exists to prevent.
func TestTheEnvelopeKeepsOneJarAcrossRequests(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if set := r.URL.Query().Get("set"); set != "" {
			http.SetCookie(w, &http.Cookie{Name: "session", Value: set, Path: "/"})
		}
		if c, err := r.Cookie("session"); err == nil {
			_, _ = w.Write([]byte("carrying " + c.Value))
			return
		}
		_, _ = w.Write([]byte("carrying nothing"))
	})

	client := arandutest.NewClient(t, handler)
	client.Get("/?set=first")
	client.Get("/").See("carrying first")
}

// ActingAs used to write a context key of its own that no policy read. It now
// writes the one auth.SubjectFrom answers, so a handler cannot tell a test
// apart from a request -- which is the only way the test proves anything.
func TestActingAsWritesTheKeyAPolicyReads(t *testing.T) {
	alice := security.Subject{ID: "u1", Tenant: "acme"}

	ctx := arandutest.ActingAs(context.Background(), alice)

	got, ok := auth.SubjectFrom(ctx)
	if !ok {
		t.Fatal("ActingAs did not write the subject under auth.WithSubject")
	}
	if got.ID != alice.ID || got.Tenant != alice.Tenant {
		t.Errorf("ActingAs carried %+v, want %+v", got, alice)
	}
}

// Subject is the reader for the above, and the pair has to agree: a reader
// looking at a different key answers false for a subject that is there.
func TestSubjectReadsBackWhatActingAsPutIn(t *testing.T) {
	alice := security.Subject{ID: "u1", Tenant: "acme"}

	got, ok := arandutest.Subject(arandutest.ActingAs(context.Background(), alice))
	if !ok || got.ID != alice.ID {
		t.Errorf("Subject read %+v (%t), want %+v", got, ok, alice)
	}

	if _, ok := arandutest.Subject(context.Background()); ok {
		t.Error("Subject claimed a subject in a context nobody wrote one into")
	}
}

// security.Subject is an alias for auth.Subject, which is what keeps ActingAs
// signature-compatible while it writes the real key. If that alias ever became
// a distinct type this line stops compiling, and it should.
var _ = auth.Subject(security.Subject{})

// Collected has to satisfy the Publisher that DrainOutbox takes. It is stated
// here because the consumers pass it as one -- arandu/tests/Feature/Relay_test.go
// and examples/tests/Feature/Relay_test.go both write &got -- and nothing in
// this package would otherwise check it.
var _ events.Publisher = (*arandutest.Collected)(nil)

// And it has to BE the hesape recorder, not a second one shaped like it.
//
// Two identical structs in two packages satisfy every assertion above and are
// still two things to keep in step. A pointer only assigns across an alias, so
// re-declaring the struct here stops the build instead of passing quietly.
var _ *hesapetest.Collected = (*arandutest.Collected)(nil)

// DrainOutbox forwards, and this is the condition that lets it.
//
// It held an implementation for as long as the outbox and the publisher were
// types of this module's own. Written as the signature hesape declares, this
// line stops compiling the day either alias is undone -- which is the day the
// forward would silently be passing something else.
var _ func(*testing.T, context.Context, *events.Outbox, events.Publisher) = hesapetest.DrainOutbox

// The hesape client answers the assertions this envelope forwards to. If any of
// them is renamed again, this fails at compile time in the package that has to
// change, rather than at the thirteen call sites that must not.
var (
	_ func(int) *hesapetest.Response    = (*hesapetest.Response)(nil).AssertStatus
	_ func() *hesapetest.Response       = (*hesapetest.Response)(nil).AssertOk
	_ func(string) *hesapetest.Response = (*hesapetest.Response)(nil).AssertSee
	_ func(string) *hesapetest.Response = (*hesapetest.Response)(nil).AssertDontSee
	_ func(string) *hesapetest.Response = (*hesapetest.Response)(nil).AssertRedirect
	_ func() string                     = (*hesapetest.Response)(nil).GetContent
	_ func(string) string               = (*hesapetest.Response)(nil).Header
)

// Collected keeps the arrival order, which is the assertion the relay tests in
// both skeletons are actually about.
func TestCollectedKeepsTheArrivalOrder(t *testing.T) {
	var got arandutest.Collected

	for _, name := range []string{"invoice.paid", "invoice.closed"} {
		if err := got.Publish(context.Background(), events.Stored{Name: name}); err != nil {
			t.Fatalf("publishing %s: %v", name, err)
		}
	}

	names := got.Names()
	if len(names) != 2 || names[0] != "invoice.paid" || names[1] != "invoice.closed" {
		t.Errorf("Collected reported %v, want [invoice.paid invoice.closed]", names)
	}
}
