package feature

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/arandu-io/framework/foundation"
	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/view"
	"github.com/arandu-io/hesape/config"
	"github.com/arandu-io/hesape/translation"
)

// catalogue is a translator that says one word of a component in Portuguese.
func catalogue() *translation.Translator {
	loader := translation.NewArrayLoader().
		AddMessages("pt-BR", "kyse", translation.Lines{"dialog.cancel": "Cancelar"}, "*")
	return translation.New(loader, "pt-BR", "en")
}

// TestEveryRequestCarriesTheConfiguredTranslator: a page drawn by a module,
// with view.New and nothing else, carries the catalogue the application was
// given, beside the name, so a component draws its own words in the request's
// language without the controller passing anything.
func TestEveryRequestCarriesTheConfiguredTranslator(t *testing.T) {
	tr := catalogue()
	m := brandModule{pages: make(chan view.Page, 1), seen: make(chan string, 2)}
	app := foundation.New(testConfig(config.EnvProd)).WithTranslator(tr).Register(m)
	if err := app.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	app.Handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://example.test/notes", nil))

	page := <-m.pages
	if page.Translator() != tr {
		t.Fatal("the page does not carry the translator the application was given")
	}
	if got := page.Translator().Get(page.Locale(), "kyse.dialog.cancel", nil); got != "Cancelar" {
		t.Errorf("the catalogue answered %q, want Cancelar", got)
	}
}

// TestNoTranslatorPutsNoneOnTheRequest: an application that names no
// catalogue leaves the request as it came, and a component draws its English.
func TestNoTranslatorPutsNoneOnTheRequest(t *testing.T) {
	handler, m := serveBrand(t, "Ledger")
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://example.test/notes", nil))
	if page := <-m.pages; page.Translator() != nil {
		t.Fatalf("Translator = %v, want none", page.Translator())
	}
}

// TestTheBuilderHandsTheTranslatorToEveryRequest: the same through Configure,
// the shape bootstrap/app.go is written in, and one catalogue only.
func TestTheBuilderHandsTheTranslatorToEveryRequest(t *testing.T) {
	tr := catalogue()
	pages := make(chan view.Page, 1)
	app := mustConfigure(t, func(b *foundation.ApplicationBuilder) {
		b.WithTranslator(tr).WithRouting(func(r *fhttp.Router) {
			r.Action(http.MethodGet, "/notes", func(ctx *fhttp.Context) error {
				pages <- view.New(ctx, "Notes")
				return ctx.Status(http.StatusOK)
			})
		})
	})
	if err := app.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	t.Cleanup(func() { _ = app.Shutdown() })
	app.Handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://example.test/notes", nil))
	if page := <-pages; page.Translator() != tr {
		t.Fatal("the page does not carry the translator WithTranslator named")
	}

	_, err := configured(t, func(b *foundation.ApplicationBuilder) { b.WithTranslator(tr).WithTranslator(tr) })
	if err == nil || !strings.Contains(err.Error(), "WithTranslator was called twice") {
		t.Fatalf("Create = %v, want the refusal of a second catalogue", err)
	}
}
