package feature

import (
	"testing"

	"github.com/arandu-io/framework/view"
	fwebhook "github.com/arandu-io/framework/webhook"
	hwebhook "github.com/arandu-io/hesape/webhook"
)

// TestTheModuleBuildsBesideTheFrameworkViews holds the dependency floor an
// application gets. An application that registers the webhook module almost
// always draws pages with framework/view, and that package converts hesape's
// view.Page to its own: a hesape this module's bridge required beyond what
// framework/view was built against would stop every such application
// compiling.
//
// The assertion is that this file compiles. It imports the view package, the
// bridge and the native webhook package against one build list, so a hesape
// requirement that framework/view does not accept fails here first.
func TestTheModuleBuildsBesideTheFrameworkViews(t *testing.T) {
	t.Parallel()

	var page view.Page
	if page.Title != "" {
		t.Fatalf("the zero page has the title %q", page.Title)
	}
	if hwebhook.DefaultPrefix == "" {
		t.Fatal("the module has no default prefix")
	}
	var module *fwebhook.Module
	if module != nil {
		t.Fatal("the zero bridge is not nil")
	}
}
