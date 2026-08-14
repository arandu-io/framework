package view_test

// What this file tests is the bridge, and only the bridge: that the old name
// reaches the new behaviour. Nothing here duplicates a test in hesape/view --
// the runtime, the registry, the asset table and the reload script are tested
// there, against the code that now runs.
//
// The two things it is actually possible to get wrong here are an alias that
// silently declares a NEW type, and a wrapper that reaches a SECOND copy of a
// package-level table. There is one compile-time assertion for the first and
// one round trip for the second.

import (
	"crypto/md5"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/view"
	hview "github.com/arandu-io/hesape/view"
)

// The aliases, asserted in both directions.
//
// One direction only would pass for a type that merely CONVERTS: a distinct
// named type with the same underlying shape is assignable in neither direction,
// but a defined type over an alias is assignable in one. Both directions is
// what says "same type".
var (
	_ view.Func        = hview.Func(nil)
	_ hview.Func       = view.Func(nil)
	_ view.LayoutFunc  = hview.LayoutFunc(nil)
	_ hview.LayoutFunc = view.LayoutFunc(nil)
	_ view.Asset       = hview.Asset{}
	_ hview.Asset      = view.Asset{}
	_ *view.Renderer   = (*hview.Renderer)(nil)
	_ *hview.Renderer  = (*view.Renderer)(nil)
)

// TestTheBridgedRendererStillSatisfiesTheFrameworkInterface is the one line
// module.go depends on and no compiler in hesape can check.
//
// hesape/view.Renderer is written against hesape/http.Renderer. This module's
// Module hands it to a kernel that asks for fhttp.Renderer, and the two
// interfaces are declared in different modules. They agree today; if either
// gains a parameter, this is where it is said out loud.
func TestTheBridgedRendererStillSatisfiesTheFrameworkInterface(t *testing.T) {
	var r fhttp.Renderer = view.NewRenderer()
	if r == nil {
		t.Fatal("NewRenderer answered nil")
	}
}

// TestTheViewRegistryIsOneTable is the failure a copied render.go would have
// produced: a view registered through the old name, and a renderer looking in
// the other map, which reads as "no view named x" over a file that is on disk.
func TestTheViewRegistryIsOneTable(t *testing.T) {
	const name = "bridge/one-table"

	view.Register(name, func(w io.Writer, data any) error {
		_, err := io.WriteString(w, "<p>"+view.Text(data)+"</p>")
		return err
	})

	var listed bool
	for _, known := range hview.Registered() {
		if known == name {
			listed = true
		}
	}
	if !listed {
		t.Fatalf("view.Register did not reach hesape's registry: %v", hview.Registered())
	}

	// And back the other way, through the bridged renderer.
	rec := httptest.NewRecorder()
	if err := view.NewRenderer().Render(t.Context(), rec, http.StatusOK, name, "hello"); err != nil {
		t.Fatal(err)
	}
	if body := rec.Body.String(); body != "<p>hello</p>" {
		t.Fatalf("the bridged renderer wrote %q", body)
	}
}

// TestTheLayoutRegistryIsOneTable: @extends compiles to RenderInto, and
// @include to Include. Both read tables RegisterLayout and Register write.
func TestTheLayoutRegistryIsOneTable(t *testing.T) {
	view.RegisterLayout("bridge/layout", func(w io.Writer, data any, sections map[string]func(io.Writer) error) error {
		if _, err := io.WriteString(w, "<main>"); err != nil {
			return err
		}
		if err := view.Yield(w, sections, "body"); err != nil {
			return err
		}
		_, err := io.WriteString(w, "</main>")
		return err
	})

	var out strings.Builder
	err := view.RenderInto(&out, "bridge/layout", nil, map[string]func(io.Writer) error{
		"body": func(w io.Writer) error { _, err := io.WriteString(w, "inside"); return err },
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "<main>inside</main>" {
		t.Fatalf("RenderInto wrote %q", got)
	}
}

// TestTheAssetTableIsOneTable is the same argument for the other registry, and
// the one with a caller outside this module: github.com/arandu-io/kyse vendors
// font faces through RegisterAsset and then reads Assets() to write the URL
// into a stylesheet. Two tables would put the face in one and serve out of the
// other, which is a 404 on an address the stylesheet had just written.
func TestTheAssetTableIsOneTable(t *testing.T) {
	const name = "bridge-probe.txt"
	body := []byte("probe")

	view.RegisterAsset(name, "text/plain; charset=utf-8", body)

	var url string
	for _, a := range hview.Assets() {
		if a.Name == name {
			url = a.URL
		}
	}
	if url == "" {
		t.Fatal("view.RegisterAsset did not reach hesape's asset table")
	}
	if want := view.AssetPath + view.AssetHash(body) + "/" + name; url != want {
		t.Fatalf("URL = %q, want %q", url, want)
	}
	if got := view.URL(name); got != url {
		t.Fatalf("view.URL = %q and view.Assets says %q", got, url)
	}
}

// builtByViewBuild stands in for assets/app.css after `aru view:build`.
var builtByViewBuild = []byte(`/*! tailwindcss v4.3.3 | MIT License | https://tailwindcss.com */
@layer utilities{.invoice-total{font-weight:600;text-align:right}}
`)

// TestTheApplicationStylesheetStillReachesTheBrowser walks the whole bridge in
// one request: the skeleton registers its stylesheet through the old name, this
// module's Module registers the route, and the bytes that come back have to be
// the ones view:build produced.
//
// It is the round trip for four wrappers at once -- RegisterStylesheet, URL,
// Handler and Version -- because they only mean anything together, and the
// defect they exist to prevent is exactly a mismatch between them: the browser
// receiving the collection's default stylesheet, md5 identical, while every
// class a project wrote did nothing and nothing failed.
func TestTheApplicationStylesheetStillReachesTheBrowser(t *testing.T) {
	before := view.URL(view.Stylesheet)

	view.RegisterStylesheet(builtByViewBuild)

	url := view.URL(view.Stylesheet)
	if url == before {
		t.Fatal("the URL did not change: it carries a content hash and is served immutable for a year")
	}

	r := fhttp.NewRouter()
	view.NewModule().Routes(r)
	server := httptest.NewServer(r)
	defer server.Close()

	resp, err := http.Get(server.URL + url)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s answered %d", url, resp.StatusCode)
	}
	if digest(got) != digest(builtByViewBuild) {
		t.Fatalf("the browser received md5 %s and view:build produced md5 %s", digest(got), digest(builtByViewBuild))
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("Content-Type = %q, want text/css", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q, want the immutable answer for a matching hash", cc)
	}
	if !strings.Contains(view.Version(), strings.TrimPrefix(url, view.AssetPath)[:12]) {
		t.Errorf("Version does not report the served stylesheet: %s", view.Version())
	}
}

// TestTheReloadScriptIsServedThroughTheBridge: ReloadTag registers itself with
// RegisterAsset, so it is the one wrapper whose correctness depends on another
// wrapper's table. The tag it answers has to name an address the module serves.
func TestTheReloadScriptIsServedThroughTheBridge(t *testing.T) {
	tag := view.ReloadTag("/_arandu/reload")
	if !strings.HasPrefix(tag, `<script src="`) {
		t.Fatalf("ReloadTag answered %q", tag)
	}
	src := strings.TrimPrefix(tag, `<script src="`)
	src, _, _ = strings.Cut(src, `"`)

	r := fhttp.NewRouter()
	view.NewModule().Routes(r)
	server := httptest.NewServer(r)
	defer server.Close()

	resp, err := http.Get(server.URL + src)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the reload script answered %d at %s", resp.StatusCode, src)
	}
	if len(body) == 0 {
		t.Fatal("the reload script answered no bytes")
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q: the tag names the script's own hash", cc)
	}
}

// TestTheOldNamesStillEscapeAndInterpolate is the smoke test for the two
// runtime wrappers a generated view calls on nearly every line.
func TestTheOldNamesStillEscapeAndInterpolate(t *testing.T) {
	if got := view.Text(42); got != "42" {
		t.Errorf("Text(42) = %q", got)
	}

	var out strings.Builder
	if err := view.CSRF(&out, view.Page{Token: "tok"}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, `value="tok"`) {
		t.Errorf("CSRF wrote %q", got)
	}
}

func digest(b []byte) string {
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}
