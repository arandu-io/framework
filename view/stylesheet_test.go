package view_test

import (
	"crypto/md5"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/arandu-io/framework/httpx"
	"github.com/arandu-io/framework/view"
)

// builtByViewBuild stands in for assets/app.css after `aru view:build`: the
// Tailwind banner the compiler preserves, plus classes that exist only because
// a project's own views used them.
//
// It is deliberately larger than the smallest thing that would prove the point,
// because the other tests in this package assert that every asset answers with
// a plausible number of bytes, and they must keep doing so whatever order the
// runner picks.
var builtByViewBuild = []byte(`/*! tailwindcss v4.3.3 | MIT License | https://tailwindcss.com */
@layer utilities{
  .invoice-table{border-collapse:collapse;width:100%}
  .invoice-table td{padding:.5rem .75rem}
  .invoice-total{font-weight:600;text-align:right}
  .overdue{color:#b91c1c}
}
`)

func digest(b []byte) string {
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}

// TestTheApplicationStylesheetIsWhatTheBrowserGets is the defect, checked by
// md5.
//
// `aru view:build` compiled resources/css/app.css into assets/app.css and
// nothing embedded or served it. The browser received the framework's
// stylesheet -- byte for byte, md5 identical -- so every class written in a
// project's own view did nothing, and no error said so anywhere.
//
// The request goes through the router with the module registered, exactly the
// way an application wires it. Calling Handler directly is what every test used
// to do, and it is why nothing noticed the routes were never registered.
func TestTheApplicationStylesheetIsWhatTheBrowserGets(t *testing.T) {
	frameworkDefault := view.URL(view.Stylesheet)

	view.RegisterStylesheet(builtByViewBuild)

	url := view.URL(view.Stylesheet)
	if url == frameworkDefault {
		t.Fatal("the URL did not change: it carries a content hash, and it is served immutable for a year -- every browser would keep the framework's stylesheet forever")
	}

	r := httpx.NewRouter()
	view.NewModule().Routes(r)
	server := httptest.NewServer(r)
	defer server.Close()

	resp, err := http.Get(server.URL + url)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s answered %d", url, resp.StatusCode)
	}
	if got, want := digest(body), digest(builtByViewBuild); got != want {
		t.Fatalf("the browser received md5 %s and view:build produced md5 %s: the application stylesheet never leaves the binary", got, want)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("Content-Type = %q, want text/css", ct)
	}
	// The hash in the path is the whole caching contract: right hash, cached
	// for a year. A body that changed under a URL that did not is a stale
	// stylesheet nobody can flush.
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q, want the immutable answer for a matching hash", cc)
	}

	// aru doctor and the debug page report what is served, not what is
	// embedded: a project that thinks it built its stylesheet has to be able to
	// see that it did.
	if !strings.Contains(view.Version(), strings.TrimPrefix(url, view.AssetPath)[:12]) {
		t.Errorf("Version does not report the served stylesheet: %s", view.Version())
	}

	// A subtest rather than a test of its own, because it asserts on what the
	// registration above left behind: run alone, there would be no first
	// stylesheet for a second one to collide with.
	t.Run("a second registration is refused", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("a second stylesheet was accepted: which one wins would be decided by import order")
			}
		}()
		view.RegisterStylesheet([]byte(".second{color:blue}"))
	})
}

// TestAnEmptyStylesheetIsRefused: a view:build that produced nothing is a
// broken build, and serving zero bytes of CSS is a page with no styles and no
// error -- exactly the failure this whole change exists to remove.
func TestAnEmptyStylesheetIsRefused(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("an empty stylesheet was accepted")
		}
	}()
	view.RegisterStylesheet(nil)
}
