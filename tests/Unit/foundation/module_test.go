package unit

import (
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

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

// TestThePublishingVocabularyIsTheHesapeVocabulary is the same assertion for the
// names a module publishes under.
//
// A module declaring Publishes() against either spelling has to satisfy both,
// and the six tags have to be the six values the engine compares against. A
// seventh tag added on one side and not the other would be a publication the
// module offers and the command refuses, with nothing between them to say so.
func TestThePublishingVocabularyIsTheHesapeVocabulary(t *testing.T) {
	for name, pair := range map[string][2]reflect.Type{
		"Publishable": {reflect.TypeFor[foundation.Publishable](), reflect.TypeFor[hfoundation.Publishable]()},
		"Publication": {reflect.TypeFor[foundation.Publication](), reflect.TypeFor[hfoundation.Publication]()},
		"PublishTag":  {reflect.TypeFor[foundation.PublishTag](), reflect.TypeFor[hfoundation.PublishTag]()},
	} {
		if pair[0] != pair[1] {
			t.Errorf("foundation.%s is %s and hesape/foundation.%s is %s", name, pair[0], name, pair[1])
		}
	}

	want := hfoundation.PublishTags()
	got := foundation.PublishTags()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PublishTags() = %v, want %v", got, want)
	}
	for _, tag := range []foundation.PublishTag{
		foundation.PublishView, foundation.PublishComponent, foundation.PublishConfig,
		foundation.PublishMigration, foundation.PublishTranslation, foundation.PublishAsset,
	} {
		if !tag.Valid() {
			t.Errorf("foundation.PublishTag(%q) is not one of the six", tag)
		}
	}
}

// TestPublicationsRefusesATagFromOutsideTheSix is the closed set, reached
// through this package rather than restated in it.
func TestPublicationsRefusesATagFromOutsideTheSix(t *testing.T) {
	_, err := foundation.Publications(publishing{
		{Tag: foundation.PublishTag("panel"), Files: fstest.MapFS{}},
	})
	if err == nil {
		t.Fatal("a tag from outside the six was accepted")
	}
	if !strings.Contains(err.Error(), "panel") {
		t.Errorf("the refusal does not name the tag: %v", err)
	}
}

// TestPublicationsAnswersNothingForAModuleThatPublishesNothing keeps the
// optional interface optional: a value that does not implement it is not an
// error.
func TestPublicationsAnswersNothingForAModuleThatPublishesNothing(t *testing.T) {
	got, err := foundation.Publications(struct{}{})
	if err != nil {
		t.Fatalf("Publications: %v", err)
	}
	if got != nil {
		t.Fatalf("Publications() = %v, want nothing", got)
	}
}

// publishing is a value that publishes exactly what it is.
type publishing []foundation.Publication

func (p publishing) Publishes() []foundation.Publication { return p }

// TestTheHesapeViewModuleSatisfiesRendererProvider is the promise the doc on
// RendererProvider makes.
//
// hesape/view.Module declares Renderer() returning a hesape/http.Renderer, and
// http.Renderer is an alias for that -- so it satisfies this interface without
// importing this package, which is what an optional interface asked for at boot
// has to mean. Break the alias and this fails here, rather than at the first
// request of an application whose pages stopped rendering.
func TestTheHesapeViewModuleSatisfiesRendererProvider(t *testing.T) {
	var _ foundation.RendererProvider = (*hview.Module)(nil)
}
