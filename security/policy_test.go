package security_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/arandu-io/framework/security"
)

type resource struct {
	owner  string
	tenant string
}

// allowOwner authorizes the owner and nobody else.
type allowOwner struct{}

func (allowOwner) Can(ctx context.Context, s security.Subject, a security.Action, r resource) error {
	if r.owner != "" && r.owner != s.ID {
		return errors.New("not the owner")
	}
	return nil
}

const (
	actionView   security.Action = "test.view"
	actionDelete security.Action = "test.delete"
)

func TestAuthorizeIssuesGrantForTheAllowedAction(t *testing.T) {
	subject := security.Subject{ID: "u1", Tenant: "t1"}

	g, err := security.Authorize(context.Background(), allowOwner{}, subject, actionView, resource{owner: "u1"})
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if err := g.Check(actionView); err != nil {
		t.Fatalf("Check on the authorized action: %v", err)
	}
	if got := g.Subject().ID; got != "u1" {
		t.Fatalf("grant subject = %q, want u1", got)
	}
	if got := g.Action(); got != actionView {
		t.Fatalf("grant action = %q, want %q", got, actionView)
	}
}

func TestAuthorizeRejectsWhenThePolicyDenies(t *testing.T) {
	subject := security.Subject{ID: "u2", Tenant: "t1"}

	_, err := security.Authorize(context.Background(), allowOwner{}, subject, actionView, resource{owner: "u1"})
	if !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("error = %v, want ErrForbidden", err)
	}
}

func TestAuthorizeRejectsAnonymousSubject(t *testing.T) {
	_, err := security.Authorize(context.Background(), allowOwner{}, security.Subject{}, actionView, resource{})
	if !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("error = %v, want ErrForbidden", err)
	}
}

// TestZeroGrantIsRejected is the runtime half of the framework thesis: the only
// Grant a caller outside this package can build is the zero value, and it never
// passes Check.
func TestZeroGrantIsRejected(t *testing.T) {
	var forged security.Grant

	err := forged.Check(actionView)
	if !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("error = %v, want ErrForbidden", err)
	}
	if got := err.Error(); !strings.Contains(got, "call security.Authorize first") {
		t.Fatalf("the message must say how to fix it, got: %s", got)
	}
}

// TestGrantIsBoundToOneAction catches the copy-paste between repository methods:
// a grant issued to view must not delete.
func TestGrantIsBoundToOneAction(t *testing.T) {
	subject := security.Subject{ID: "u1", Tenant: "t1"}
	g, err := security.Authorize(context.Background(), allowOwner{}, subject, actionView, resource{owner: "u1"})
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}

	if err := g.Check(actionDelete); !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("error = %v, want ErrForbidden", err)
	}
}

func TestSystemGrantCarriesTheTenant(t *testing.T) {
	g := security.SystemGrant(actionView, "t9")

	if err := g.Check(actionView); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got := g.Subject().Tenant; got != "t9" {
		t.Fatalf("tenant = %q, want t9", got)
	}
	if !g.Subject().HasRole("system") {
		t.Fatal("the system subject must carry the system role, so audits can find it")
	}
}

func TestHasRole(t *testing.T) {
	s := security.Subject{ID: "u1", Roles: []string{"admin", "billing"}}

	if !s.HasRole("billing") {
		t.Fatal("HasRole(billing) = false, want true")
	}
	if s.HasRole("Admin") {
		t.Fatal("HasRole must be case sensitive: roles are identifiers, not prose")
	}
}

// TestSystemGrantRequiresATenant is RULE 14 in the type system: a system grant
// with no tenant would read across every customer of the system, so it is not
// expressible -- the empty tenant yields the zero Grant, which fails Check.
func TestSystemGrantRequiresATenant(t *testing.T) {
	g := security.SystemGrant(actionView, "")

	if err := g.Check(actionView); !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("error = %v, want ErrForbidden", err)
	}
	if g.Subject().Tenant != "" {
		t.Fatal("a refused system grant must carry no subject at all")
	}
}

// TestARefusedSystemGrantSaysWhy is a bug an audit found in the message rather
// than in the behaviour.
//
// SystemGrant refuses an empty or malformed tenant by returning the zero Grant,
// which is correct. But Check then produced the zero Grant's message -- "call
// security.Authorize first" -- and in a job or a scheduled task there is no
// request to authorize from, so the advice is impossible to follow and points
// away from the actual cause.
func TestARefusedSystemGrantSaysWhy(t *testing.T) {
	cases := []struct {
		what   string
		tenant string
		says   string
	}{
		{"no tenant at all", "", "with no tenant"},
		{"a tenant that would escape its namespace", "acme/reports", "cannot be one"},
		{"a tenant carrying the key separator", "acme:session", "cannot be one"},
	}

	for _, c := range cases {
		t.Run(c.what, func(t *testing.T) {
			g := security.SystemGrant("invoice.send", c.tenant)
			err := g.Check("invoice.send")
			if err == nil {
				t.Fatal("the grant passed Check")
			}
			if !errors.Is(err, security.ErrForbidden) {
				t.Errorf("error = %v, want ErrForbidden", err)
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("the message does not name the cause:\n%v", err)
			}
			if strings.Contains(err.Error(), "security.Authorize") {
				t.Errorf("the message still sends the reader to Authorize, which a job cannot call:\n%v", err)
			}
		})
	}
}

// TestTheZeroGrantStillSaysAuthorize: the other message is right for the other
// mistake, and this keeps the fix above from swallowing it.
func TestTheZeroGrantStillSaysAuthorize(t *testing.T) {
	var g security.Grant
	err := g.Check("invoice.send")
	if err == nil || !strings.Contains(err.Error(), "security.Authorize") {
		t.Fatalf("the zero Grant no longer points at Authorize: %v", err)
	}
}
