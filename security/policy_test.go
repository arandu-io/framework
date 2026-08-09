package security_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/arandu-io/framework/security"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
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

// TestTheGrantDocNamesEveryDoor keeps the central comment honest.
//
// The doc on Grant is where a reader checks the thesis, and it said for months
// that there is "no public constructor other than Authorize". That was never
// true -- SystemGrant is right below it in this file -- and it read as a
// compile-time guarantee for something `aru doctor` enforces as a lint. The
// gap only surfaced when jobs.GrantFor turned out to wrap SystemGrant and to be
// invisible to every rule.
//
// So: every exported function in this module that hands back a Grant has to be
// named in that comment. Adding a door costs writing it down where the promise
// is made.
func TestTheGrantDocNamesEveryDoor(t *testing.T) {
	fset := token.NewFileSet()

	var grantDoc string
	doors := map[string]string{} // symbol -> file

	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "testdata" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, perr := parser.ParseFile(fset, path, nil, parser.ParseComments|parser.SkipObjectResolution)
		if perr != nil {
			return nil
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if ok && ts.Name.Name == "Grant" && d.Doc != nil {
						grantDoc = d.Doc.Text()
					}
				}
			case *ast.FuncDecl:
				if d.Recv != nil || !d.Name.IsExported() || d.Type.Results == nil {
					continue
				}
				for _, r := range d.Type.Results.List {
					if returnsGrant(r.Type) {
						doors[d.Name.Name] = path
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if grantDoc == "" {
		t.Fatal("Grant has no doc comment: the central promise of the framework is undocumented")
	}
	if len(doors) < 2 {
		t.Fatalf("found %d exported Grant constructors: this test stopped testing anything", len(doors))
	}
	for name, path := range doors {
		if !strings.Contains(grantDoc, name) {
			t.Errorf("%s (%s) hands back a Grant and the doc on Grant does not name it", name, path)
		}
	}
}

// returnsGrant reports whether a result type is a Grant, here or through the
// security qualifier another package would write.
func returnsGrant(e ast.Expr) bool {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name == "Grant"
	case *ast.SelectorExpr:
		return t.Sel.Name == "Grant"
	}
	return false
}

// A guest reaches the policy; a subject nobody filled in does not.
//
// The difference is the whole design. An empty Subject is almost always a
// session that was not loaded, and asking a policy about nobody is how a hole
// gets opened by an accident. A guest is the same emptiness declared on purpose,
// and the policy decides about it like any other subject.
func TestAGuestReachesThePolicyAndAnEmptySubjectDoesNot(t *testing.T) {
	ctx := context.Background()

	// The zero value: refused before the policy is consulted, which is what
	// stops a forgotten session load from becoming an authorization question.
	if _, err := security.Authorize(ctx, &askedPolicy{}, security.Subject{}, "post.view", 0); err == nil {
		t.Fatal("an empty subject reached the policy")
	}

	// The guest: consulted, and refused by the policy rather than before it.
	asked := &askedPolicy{}
	if _, err := security.Authorize(ctx, asked, security.Guest("t1"), "post.view", 0); err == nil {
		t.Fatal("the policy denied and Authorize returned a grant")
	}
	if !asked.called {
		t.Error("the policy was never asked about the guest")
	}
}

// A policy that opens an action to guests gets a Grant, and the Grant works.
func TestAPolicyCanOpenAnActionToGuests(t *testing.T) {
	g, err := security.Authorize(context.Background(), publicPolicy{}, security.Guest("t1"), "post.view", 0)
	if err != nil {
		t.Fatalf("a policy allowed a guest and Authorize refused: %v", err)
	}
	if err := g.Check("post.view"); err != nil {
		t.Errorf("the grant a guest received does not work: %v", err)
	}
	// The tenant is the application's, not the visitor's: RULE 14 is not
	// suspended because nobody signed in. data.Tenant reads it off the Grant,
	// and that is checked where data can be imported without a cycle.
}

// askedPolicy denies everything and records that it was consulted.
type askedPolicy struct{ called bool }

func (p *askedPolicy) Can(context.Context, security.Subject, security.Action, int) error {
	p.called = true
	return errors.New("no")
}

// publicPolicy is what an application writes to serve a page to a reader.
type publicPolicy struct{}

func (publicPolicy) Can(_ context.Context, s security.Subject, a security.Action, _ int) error {
	if s.IsGuest() && a == "post.view" {
		return nil
	}
	return errors.New("not public")
}
