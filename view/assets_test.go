package view_test

import (
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/arandu-io/framework/httpx"
	"github.com/arandu-io/framework/view"
)

// These three guards moved here from the porang repository when it dissolved
// (ADR 0021). They protect the embedded assets, and the assets now live in this
// package -- a guard in an archived repository guards nothing.

// TestNoNodeAnywhere is RULE 13, checked rather than promised.
//
// A project runs with `git clone && aru dev`. No node_modules, no package.json,
// no JS lockfile, and no Node installed. In Laravel, Node entered through the
// error page -- Illuminate/Foundation/resources/exceptions/renderer/ carries a
// package.json and a vite.config.js. Ours is html/template, inline.
func TestNoNodeAnywhere(t *testing.T) {
	forbidden := []string{"package.json", "package-lock.json", "yarn.lock",
		"pnpm-lock.yaml", "bun.lockb", "node_modules", "vite.config.js", "vite.config.ts"}

	// ".." and not ".": the whole repository, because the promise is about the
	// repository. The reference projects cloned next to it are full of
	// package.json and are read-only material rather than code that ships
	// (RULE 7), which is why the walk starts here and not one level higher.
	root := ".."
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if strings.Contains(path, "/.git/") {
			return nil
		}
		for _, name := range forbidden {
			if d.Name() == name {
				t.Errorf("%s exists: the promise is that a project runs without Node", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestNoExternalOrigin: the CSP the framework sets is script-src 'self', so an
// asset pointing at a CDN is a script that never loads -- and the only sign is a
// console message nobody sees until the page is already broken.
func TestNoExternalOrigin(t *testing.T) {
	external := regexp.MustCompile(`(?i)(https?:)?//(cdn|unpkg|jsdelivr|googleapis|cloudflare)`)

	entries, err := os.ReadDir("assets")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no assets are embedded")
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join("assets", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		// A URL inside a comment or a source map is not a load. What matters is
		// a reference the browser would fetch.
		for _, line := range strings.Split(string(b), "\n") {
			if strings.Contains(line, "sourceMappingURL") {
				continue
			}
			if loc := external.FindString(line); loc != "" && strings.Contains(line, "src=") {
				t.Errorf("%s points at %s: the CSP is script-src 'self'", e.Name(), loc)
			}
		}
	}
}

// TestTheCSSIsCompiled: app.css has to be Tailwind's output, not its input. The
// source has @import and @source directives no browser understands, and shipping
// it produces a page with no styles at all and no error anywhere.
func TestTheCSSIsCompiled(t *testing.T) {
	compiled, err := os.ReadFile(filepath.Join("assets", "app.css"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(compiled), `@import "tailwindcss"`) {
		t.Error("assets/app.css is the source, not the build: run the standalone tailwindcss over app.src.css")
	}
	if len(compiled) < 2000 {
		t.Errorf("assets/app.css is %d bytes, too small to be a compiled stylesheet", len(compiled))
	}
}

// TestEveryAssetIsServed is the check whose absence let the assets 404 for
// weeks: every embedded file has to come back over HTTP, with its bytes.
//
// porang.Mount existed and had zero call sites. Every test called the renderer
// directly and none went through the module, so nothing noticed.
func TestEveryAssetIsServed(t *testing.T) {
	// Through the router, with the module registered, exactly as an application
	// wires it. Calling Handler directly is what every test used to do, and it
	// is why nothing noticed that the routes were never registered.
	r := httpx.NewRouter()
	view.NewModule().Routes(r)

	server := httptest.NewServer(r)
	defer server.Close()

	for _, name := range []string{"app.css", "htmx.min.js", "alpine.min.js"} {
		url := view.URL(name)
		if url == "" {
			t.Errorf("%s has no URL", name)
			continue
		}

		resp, err := http.Get(server.URL + url)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s answered %d at %s", name, resp.StatusCode, url)
		}
		if len(body) < 100 {
			t.Errorf("%s answered %d bytes", name, len(body))
		}
	}
}
