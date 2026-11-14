package feature

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/arandu-io/framework/foundation"
	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/view"
	"github.com/arandu-io/hesape/config"
	"github.com/arandu-io/hesape/support"
	hview "github.com/arandu-io/hesape/view"
)

// brandedApp boots an Application named Peráta that serves the assets and
// rejects a form, with a middleware of its own that marks every response that
// went through the application's pipeline.
func brandedApp(t *testing.T) http.Handler {
	t.Helper()
	cfg := testConfig(config.EnvProd)
	cfg.Session.Secure = true
	cfg.Brand = support.NewBrand("Peráta")
	t.Cleanup(func() { hview.UseBrand(support.Brand{}) })

	k := foundation.New(cfg).Register(view.NewModule(), flashModule{seen: make(chan fhttp.State, 1)})
	if err := k.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	return k.Handler()
}

// TestANamedApplicationNamesItsFlashAfterItself: the Application's own flash
// takes the brand, so a rejected form leaves perata_flash and no cookie of the
// framework's name.
func TestANamedApplicationNamesItsFlashAfterItself(t *testing.T) {
	handler := brandedApp(t)

	post := httptest.NewRequest(http.MethodPost, "http://example.test/posts", strings.NewReader(url.Values{"title": {""}}.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.Header.Set("Referer", "http://example.test/posts/new")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, post)

	var names []string
	for _, c := range rec.Result().Cookies() {
		names = append(names, c.Name)
	}
	if len(names) != 1 || names[0] != "perata_flash" {
		t.Fatalf("cookies = %v, want only perata_flash", names)
	}
}

// TestANamedApplicationServesItsAssetsUnderItsOwnPrefix: the address a page
// carries is /_perata/assets/, answered by the framework's own route, and the
// old address keeps answering for the pages cached before.
func TestANamedApplicationServesItsAssetsUnderItsOwnPrefix(t *testing.T) {
	handler := brandedApp(t)

	url := hview.Asset("theme.js")
	if !strings.HasPrefix(url, "/_perata/assets/") {
		t.Fatalf("Asset = %q", url)
	}
	for _, path := range []string{url, strings.Replace(url, "/_perata/", "/_arandu/", 1)} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://example.test"+path, nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "perata.theme") {
			t.Errorf("GET %s answered %d, want the script with the application's key", path, rec.Code)
		}
	}
}

// TestARouteOfTheApplicationCannotAnswerUnderTheBrandsPrefix: the namespace the
// brand's assets live in is reserved with the framework's own rules.
func TestARouteOfTheApplicationCannotAnswerUnderTheBrandsPrefix(t *testing.T) {
	cfg := testConfig(config.EnvProd)
	cfg.Brand = support.NewBrand("Peráta")
	t.Cleanup(func() { hview.UseBrand(support.Brand{}) })

	k := foundation.New(cfg).Register(prefixModule{})
	if err := k.Boot(context.Background()); err == nil || !strings.Contains(err.Error(), "/_perata/") {
		t.Fatalf("Boot = %v, want a refusal naming /_perata/", err)
	}
}

type prefixModule struct{}

func (prefixModule) Name() string { return "intruder" }
func (prefixModule) Routes(r *fhttp.Router) {
	r.Get("/_perata/anything", func(w http.ResponseWriter, _ *http.Request) {})
}
