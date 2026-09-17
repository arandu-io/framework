package unit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	fgeo "github.com/arandu-io/framework/geo"
	fhttp "github.com/arandu-io/framework/http"
	hgeo "github.com/arandu-io/hesape/geo"
	"github.com/arandu-io/hesape/routing"
)

var (
	_ fgeo.Config        = hgeo.Config{}
	_ hgeo.Config        = fgeo.Config{}
	_ fgeo.Document      = hgeo.Document{}
	_ hgeo.Document      = fgeo.Document{}
	_ fgeo.Metadata      = hgeo.Metadata{}
	_ hgeo.Metadata      = fgeo.Metadata{}
	_ fgeo.MetadataInput = hgeo.MetadataInput{}
	_ hgeo.MetadataInput = fgeo.MetadataInput{}
)

func TestConstantsAndErrorsAreTheNativeGeoValues(t *testing.T) {
	if fgeo.AllSurfaces != hgeo.AllSurfaces || fgeo.GenerativeSurfaces != hgeo.GenerativeSurfaces || fgeo.SearchSurfaces != hgeo.SearchSurfaces {
		t.Fatal("surface sets diverged from Hesape GEO")
	}
	if fgeo.ErrInvalidConfig != hgeo.ErrInvalidConfig || fgeo.ErrCapacity != hgeo.ErrCapacity {
		t.Fatal("GEO errors are not the Hesape values")
	}
}

func TestFrameworkEnvelopeServesTheNativeGeoHandlers(t *testing.T) {
	config := fgeo.Config{Enabled: true, Indexing: false, Surfaces: fgeo.Robots.AsSet() | fgeo.LLMs.AsSet()}
	module := fgeo.NewModule(config, nil)
	if err := module.Boot(context.Background()); err != nil {
		t.Fatal(err)
	}
	frameworkRouter := fhttp.NewRouter().ForModule(module.Name())
	module.Routes(frameworkRouter)

	native := hgeo.NewModule(config, nil)
	if err := native.Boot(context.Background()); err != nil {
		t.Fatal(err)
	}
	nativeRouter := routing.NewRouter().ForModule(native.Name())
	native.Routes(nativeRouter)

	for _, path := range []string{"/robots.txt", "/llms.txt"} {
		got := request(t, frameworkRouter, path)
		want := request(t, nativeRouter, path)
		if got.Code != want.Code || got.Body.String() != want.Body.String() || got.Header().Get("Content-Type") != want.Header().Get("Content-Type") {
			t.Fatalf("%s bridge response diverged\nframework: %d %q\nnative: %d %q", path, got.Code, got.Body.String(), want.Code, want.Body.String())
		}
	}

	for _, route := range frameworkRouter.Routes() {
		if route.Module != "geo" {
			t.Fatalf("route %s module = %q", route.Pattern, route.Module)
		}
	}
}

func request(t *testing.T, handler http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://attacker.invalid"+path, nil))
	return recorder
}

func TestMetadataWrapperReachesHesape(t *testing.T) {
	config := fgeo.Config{Enabled: true, Indexing: true, Origin: "https://example.test"}
	input := fgeo.MetadataInput{Path: "/guide", Title: "Guide", SiteName: "Example", Indexable: true}
	got, err := fgeo.MetadataFor(config, input)
	if err != nil {
		t.Fatal(err)
	}
	want, err := hgeo.MetadataFor(config, input)
	if err != nil {
		t.Fatal(err)
	}
	if got.JSON() != want.JSON() {
		t.Fatal("metadata wrapper does not reach Hesape GEO")
	}
}
