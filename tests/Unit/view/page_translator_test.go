package unit

import (
	"net/http"
	"net/http/httptest"
	"testing"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/view"
	"github.com/arandu-io/hesape/translation"
)

// translatorPage is what a component library asks a page for when it draws a
// word nobody wrote. Declared here, by the consumer, so the test proves Page
// answers it without importing the library.
type translatorPage interface {
	Translator() *translation.Translator
	Locale() string
}

var _ translatorPage = view.Page{}

// TestNewCarriesTheTranslatorAndTheLocaleOfTheRequest: a screen that embeds
// this Page says a component's own words in the request's language, exactly as
// one that embeds hesape's does.
func TestNewCarriesTheTranslatorAndTheLocaleOfTheRequest(t *testing.T) {
	loader := translation.NewArrayLoader().
		AddMessages("pt-BR", "kyse", translation.Lines{"dialog.cancel": "Cancelar"}, "*")
	tr := translation.New(loader, "en", "en")

	var page view.Page
	r := fhttp.NewRouter()
	r.Action(http.MethodGet, "/notes", func(ctx *fhttp.Context) error {
		page = view.New(ctx, "Notes")
		return nil
	})
	req := httptest.NewRequest(http.MethodGet, "/notes", nil)
	ctx := translation.WithLocale(translation.WithTranslator(req.Context(), tr), "pt-BR")
	r.ServeHTTP(httptest.NewRecorder(), req.WithContext(ctx))

	if page.Translator() != tr {
		t.Fatal("Translator is not the one on the request")
	}
	if page.Locale() != "pt-BR" {
		t.Fatalf("Locale = %q, want pt-BR", page.Locale())
	}
	if got := page.Translator().Get(page.Locale(), "kyse.dialog.cancel", nil); got != "Cancelar" {
		t.Errorf("the catalogue answered %q, want Cancelar", got)
	}

	other := translation.New(translation.NewArrayLoader(), "en", "en")
	if with := page.WithTranslator(other, "en"); with.Translator() != other || with.Locale() != "en" {
		t.Error("WithTranslator did not replace the catalogue and the locale")
	}
	if page.Translator() != tr {
		t.Error("WithTranslator changed the page it was called on")
	}
}
