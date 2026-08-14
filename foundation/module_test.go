package foundation_test

import (
	"reflect"
	"testing"

	"github.com/arandu-io/framework/foundation"
	hfoundation "github.com/arandu-io/hesape/foundation"
	hview "github.com/arandu-io/hesape/view"
)

// TestTheVocabularyIsTheHesapeVocabulary is the alias half of this package.
//
// Every line is a compile-time assertion that the two names are one type. A
// rename in github.com/arandu-io/hesape that this package has not followed
// fails here rather than in the repositories that import the old names through
// the kernel bridge.
func TestTheVocabularyIsTheHesapeVocabulary(t *testing.T) {
	for name, pair := range map[string][2]reflect.Type{
		"Bootable":     {reflect.TypeFor[foundation.Bootable](), reflect.TypeFor[hfoundation.Bootable]()},
		"Background":   {reflect.TypeFor[foundation.Background](), reflect.TypeFor[hfoundation.Background]()},
		"Closable":     {reflect.TypeFor[foundation.Closable](), reflect.TypeFor[hfoundation.Closable]()},
		"Diagnostic":   {reflect.TypeFor[foundation.Diagnostic](), reflect.TypeFor[hfoundation.Diagnostic]()},
		"Health":       {reflect.TypeFor[foundation.Health](), reflect.TypeFor[hfoundation.Health]()},
		"Schedulable":  {reflect.TypeFor[foundation.Schedulable](), reflect.TypeFor[hfoundation.Schedulable]()},
		"Migratable":   {reflect.TypeFor[foundation.Migratable](), reflect.TypeFor[hfoundation.Migratable]()},
		"Migration":    {reflect.TypeFor[foundation.Migration](), reflect.TypeFor[hfoundation.Migration]()},
		"Task":         {reflect.TypeFor[foundation.Task](), reflect.TypeFor[hfoundation.Task]()},
		"Scope":        {reflect.TypeFor[foundation.Scope](), reflect.TypeFor[hfoundation.Scope]()},
		"ReloadTagger": {reflect.TypeFor[foundation.ReloadTagger](), reflect.TypeFor[hfoundation.ReloadTagger]()},
	} {
		if pair[0] != pair[1] {
			t.Errorf("foundation.%s is %s and hesape/foundation.%s is %s", name, pair[0], name, pair[1])
		}
	}

	if foundation.Global != hfoundation.Global || foundation.PerTenant != hfoundation.PerTenant {
		t.Error("the Scope constants stopped being the hesape ones")
	}
}

// TestTheHesapeViewModuleSatisfiesRendererProvider is the promise the doc on
// RendererProvider makes.
//
// hesape/view.Module declares Renderer() returning a hesape/http.Renderer, and
// httpx.Renderer is an alias for that -- so it satisfies this interface without
// importing this package, which is what an optional interface asked for at boot
// has to mean. Break the alias and this fails here, rather than at the first
// request of an application whose pages stopped rendering.
func TestTheHesapeViewModuleSatisfiesRendererProvider(t *testing.T) {
	var _ foundation.RendererProvider = (*hview.Module)(nil)
}
