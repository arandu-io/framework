package unit

import (
	"testing"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/security"
)

// TestARouterWithNoFlashAnswersNil is the one case in which a module needs a
// flash of its own: a router built by hand, as a test builds one.
func TestARouterWithNoFlashAnswersNil(t *testing.T) {
	if f := fhttp.NewRouter().Flash(); f != nil {
		t.Fatalf("NewRouter().Flash() = %p, want nil", f)
	}
}

// TestTheRouterHandsOutTheFlashItWasWiredWith keeps a module from building a
// second flash: whatever router it is given, at whatever depth, answers the one
// the router writes its rejections with.
func TestTheRouterHandsOutTheFlashItWasWiredWith(t *testing.T) {
	f := security.NewFlash(make([]byte, 32), true)
	r := fhttp.NewRouter().WithFlash(f)

	for name, sub := range map[string]*fhttp.Router{
		"WithFlash":           r,
		"Group":               r.Group("/admin"),
		"Group of a Group":    r.Group("/admin").Group("/users"),
		"ForModule":           r.ForModule("billing"),
		"ForModule and Group": r.ForModule("billing").Group("/invoices"),
		"WithRenderer":        r.WithRenderer(nil),
	} {
		if got := sub.Flash(); got != f {
			t.Errorf("%s: Flash() = %p, want the wired flash %p", name, got, f)
		}
	}
}

// TestRewiringARouterLeavesTheOriginalsFlash pins the copy: WithFlash returns
// a new router, so a module that rewires the one it was given -- directly or
// below a Group -- cannot change what that router hands to anyone else.
func TestRewiringARouterLeavesTheOriginalsFlash(t *testing.T) {
	parent := security.NewFlash(make([]byte, 32), true)
	other := security.NewFlash(make([]byte, 32), false)
	r := fhttp.NewRouter().WithFlash(parent)

	rewired := r.WithFlash(other)
	child := r.Group("/auth").WithFlash(other)

	for name, got := range map[string]*security.Flash{"rewired": rewired.Flash(), "child": child.Flash()} {
		if got != other {
			t.Errorf("%s Flash() = %p, want %p", name, got, other)
		}
	}
	if got := r.Flash(); got != parent {
		t.Errorf("original Flash() = %p after rewiring, want %p", got, parent)
	}
}
