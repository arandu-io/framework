package unit

import (
	"reflect"
	"slices"
	"testing"

	"github.com/arandu-io/framework/validation"
	"github.com/arandu-io/framework/view"
	hview "github.com/arandu-io/hesape/view"
)

// The layout a project publishes asserts its data to hesape's view.Layout --
// the type the view compiler names when a layout declares none -- and hands
// itself to components.Toast, which asks a page for Flashed, FieldError and
// OldOr. A layout written against this package's Layout has to be the same
// layout, or the tray compiles against one import and not the other.
func TestLayoutIsHesapeLayout(t *testing.T) {
	ours := reflect.TypeOf((*view.Layout)(nil)).Elem()
	native := reflect.TypeOf((*hview.Layout)(nil)).Elem()
	if ours != native {
		t.Fatalf("view.Layout is %s, a type of its own; want hesape's %s, so one layout fits both imports", ours, native)
	}
}

// The compiled layout receives its data as any and asserts it. A Page that
// misses one method of the contract fails that assertion at run time, on
// every page, with nothing failing to build -- so the assertion is made here
// exactly the way the generated layout makes it.
func TestAPageReachesALayoutTheWayACompiledLayoutAssertsIt(t *testing.T) {
	var data any = view.Page{Title: "Posts"}
	layout, ok := data.(hview.Layout)
	if !ok {
		t.Fatal("view.Page does not satisfy hesape's view.Layout, so every compiled layout answers WrongData for it")
	}
	if got := layout.PageTitle(); got != "Posts" {
		t.Errorf("PageTitle = %q, want Posts", got)
	}
}

func TestFlashedAnswersWhatTheRequestBeforeLeftUnderTheKey(t *testing.T) {
	page := view.Page{Messages: map[string]string{"status": "Saved."}}

	if got := page.Flashed("status"); got != "Saved." {
		t.Errorf(`Flashed("status") = %q, want the sentence the redirecting request left`, got)
	}
	if got := page.Flashed("warning"); got != "" {
		t.Errorf(`Flashed("warning") = %q, want empty for a key nobody flashed`, got)
	}
	if got := (view.Page{}).Flashed("status"); got != "" {
		t.Errorf(`Flashed on a page with no flash = %q, want empty`, got)
	}
}

// Any and All are the layout's two questions under hesape's names. They answer
// what HasErrors and ErrorSummary answer, because a page asked one way and the
// banner drawn the other must not disagree.
func TestAnyAndAllAnswerWhatHasErrorsAndErrorSummaryDo(t *testing.T) {
	rejected := view.Page{Errors: validation.Errors{
		"password": {"must be at least 12 characters"},
		"email":    {"is required"},
	}}
	if !rejected.Any() {
		t.Error("Any said no on a page that was rejected")
	}
	if got, want := rejected.All(), rejected.ErrorSummary(); !slices.Equal(got, want) || len(got) != 2 {
		t.Errorf("All = %q, want ErrorSummary's %q", got, want)
	}

	fresh := view.Page{}
	if fresh.Any() {
		t.Error("Any said yes on a page nobody was rejected on")
	}
	if got := fresh.All(); got != nil {
		t.Errorf("All = %q on a fresh page, want nil so the banner is not drawn empty", got)
	}
}
