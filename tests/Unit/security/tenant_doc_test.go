// The guard on one doc comment, because that comment is an instruction.
//
// Tenant's doc is where somebody looks to find out what the tenant reaches. It
// once said that a lock name takes the value, and a lock name does not: locks
// are named as given, on purpose, so that one replica runs a scheduled task
// instead of one replica per customer running it. A reader who trusted the
// sentence and made the lock consistent with the cache keys would turn the thing
// the lock prevents into the thing it does.
//
// So the sentence is load-bearing, and it is worth a test that fails when it is
// rewritten away. Reading the doc comment out of the source is the only way to
// assert on prose: nothing about it reaches the compiler.

package unit

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	testbase "github.com/arandu-io/framework/tests"
)

// notScopedByTenant are the phrases the doc has to keep.
//
// Each names one of the three keys that are not built from the tenant. They are
// exact substrings rather than a description, because a check that accepts any
// wording accepts the wording that dropped the exception.
var notScopedByTenant = []string{
	"A lock name is used as given",
	"A rate limit key is built from the address or the session id",
	"A session id is what says which tenant a request belongs to",
}

// tenantScopePromises are the ways this comment says a key is built from the
// tenant. A sentence carrying one of them may not also name a lock.
var tenantScopePromises = []string{
	"is built from this value",
	"are built from this value",
	"takes this value",
	"take this value",
}

// TestTenantDocKeepsTheThreeKeysItDoesNotScope fails when the exceptions are
// summarized away.
//
// The comment is shorter and prettier without them, which is exactly why it was
// shorter and prettier once: "statement, cache key, storage path and lock name"
// reads as a complete list and is wrong in its fourth item.
func TestTenantDocKeepsTheThreeKeysItDoesNotScope(t *testing.T) {
	for _, phrase := range droppedExceptions(tenantDoc(t)) {
		t.Errorf("the doc comment of security.Tenant no longer says %q.\n"+
			"Each of the three is a key that is deliberately not built from the tenant, and this comment is where a reader finds that out. "+
			"Dropping one leaves the reader to make it consistent with the cache keys, which for the lock is the failure it exists to prevent.", phrase)
	}
}

// TestTenantDocDoesNotPromiseTenantScopeForLocks fails when a lock is put back
// into the list of things the value reaches.
//
// It reads one sentence at a time rather than the whole comment, because the
// comment names locks on purpose -- to say they are not scoped. What may not
// happen is a lock and a promise in the same sentence.
func TestTenantDocDoesNotPromiseTenantScopeForLocks(t *testing.T) {
	for _, sentence := range lockPromises(tenantDoc(t)) {
		t.Errorf("the doc comment of security.Tenant promises tenant scope for a lock:\n\n\t%s\n\n"+
			"It has none. A lock is named as given so that one replica runs the work; one lock per tenant is one replica per customer running it, which is the duplicate work the lock is taken to prevent.", sentence)
	}
}

// TestTheGuardRejectsTheSentenceItReplaced is the guard checked against the
// comment it exists for.
//
// Without it the two tests above pass on any text that happens to carry the
// three phrases, and a check that never refused anything is a check nobody can
// tell is working.
func TestTheGuardRejectsTheSentenceItReplaced(t *testing.T) {
	const replaced = "Tenant returns the tenant the Grant was issued for. " +
		"Every tenant-scoped statement, cache key, storage path and lock name takes this value, never one that arrived with the request."

	if got := lockPromises(replaced); len(got) == 0 {
		t.Error("the sentence that named a lock among the things that take the tenant was accepted")
	}
	if got := droppedExceptions(replaced); len(got) != len(notScopedByTenant) {
		t.Errorf("a comment naming none of the three exceptions was reported as dropping %d of them, want %d", len(got), len(notScopedByTenant))
	}
}

// droppedExceptions answers the phrases doc no longer carries.
func droppedExceptions(doc string) []string {
	var dropped []string
	for _, phrase := range notScopedByTenant {
		if !strings.Contains(doc, phrase) {
			dropped = append(dropped, phrase)
		}
	}
	return dropped
}

// lockPromises answers the sentences of doc that name a lock and promise it the
// tenant.
func lockPromises(doc string) []string {
	var found []string
	for _, sentence := range strings.Split(doc, ". ") {
		if !strings.Contains(strings.ToLower(sentence), "lock") {
			continue
		}
		for _, promise := range tenantScopePromises {
			if strings.Contains(sentence, promise) {
				found = append(found, strings.TrimSpace(sentence))
				break
			}
		}
	}
	return found
}

// tenantDoc reads the doc comment of security.Tenant out of the source.
//
// The file is found from the module root rather than by a relative path: a
// relative path is correct only from the depth it was written at, and a test
// that moves keeps passing while reading nothing.
//
// Newlines become spaces, so a sentence wrapped across two lines is still one
// sentence to the checks above.
func tenantDoc(t *testing.T) string {
	t.Helper()

	path := filepath.Join(testbase.ModuleRoot(t), "security", "auth.go")
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Name.Name != "Tenant" {
			continue
		}
		if fn.Doc == nil {
			t.Fatal("security.Tenant carries no doc comment, so what it promises about the tenant is unwritten")
		}
		return strings.Join(strings.Fields(fn.Doc.Text()), " ")
	}

	t.Fatalf("no func Tenant in %s", path)
	return ""
}
