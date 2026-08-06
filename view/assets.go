// Package view is the view layer: kyse for markup, HTMX for interaction,
// Alpine for ephemeral client state, Tailwind for style. It is a binary and it
// is never Node.
//
// A project that uses it still runs with `git clone && aru dev`: no
// node_modules, no package.json, no lockfile of JavaScript, nothing installed
// beyond Go and the standalone binaries the CLI fetches. Having a build step is
// allowed; being Node is not (RULE 13).
//
// It lives in the framework and the views do not. That is the split the Laravel
// mirror asks for: resources/views/ belongs to laravel/laravel, the rendering
// machinery to laravel/framework. It used to be a repository of its own,
// `porang`, and dissolving it is ADR 0021.
//
// The error page deliberately does not use this package. It has to render when
// the rest is broken, including when the view build failed, so it stays as
// html/template inline in observability/errorpage.
package view

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
)

// The scripts are embedded, not fetched.
//
// This is not preference. The SecurityHeaders middleware sets
// "script-src 'self'", so loading HTMX from a CDN would mean loosening the CSP
// -- paying in security for convenience. It also means the deploy stays one
// binary: no asset publishing step, no CDN to invalidate, no storage:link.
//
//go:embed assets/htmx.min.js assets/alpine.min.js assets/app.css
var files embed.FS

// AssetPath is where assets are served from. The hash is in the path, so the
// response can be cached forever and a new build simply has a new URL.
const AssetPath = "/_arandu/assets/"

// asset is one embedded file with its content hash.
type asset struct {
	name        string
	contentType string
	body        []byte
	hash        string
}

var assets = map[string]*asset{}

func init() {
	for name, contentType := range map[string]string{
		"htmx.min.js":   "application/javascript; charset=utf-8",
		"alpine.min.js": "application/javascript; charset=utf-8",
		"app.css":       "text/css; charset=utf-8",
	} {
		body, err := files.ReadFile("assets/" + name)
		if err != nil {
			// The file is embedded at build time: if it is missing, the binary is
			// broken and there is nothing to recover from at runtime.
			panic("porang: missing embedded asset " + name + ": " + err.Error())
		}
		sum := sha256.Sum256(body)
		assets[name] = &asset{
			name:        name,
			contentType: contentType,
			body:        body,
			hash:        hex.EncodeToString(sum[:])[:12],
		}
	}
}

// URL returns the versioned path of an asset: /_arandu/assets/<hash>/htmx.min.js
//
// The hash comes from the content, so upgrading HTMX changes the URL and no
// browser serves a stale script -- without anyone remembering to bump a version.
func URL(name string) string {
	a, ok := assets[name]
	if !ok {
		return AssetPath + "missing/" + name
	}
	return AssetPath + a.hash + "/" + a.name
}

// Handler serves the embedded assets.
//
// Anything whose path carries the right hash is immutable and cached for a year;
// a wrong hash is served without caching, so a stale reference degrades into a
// slow page rather than a broken one.
func Handler(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, AssetPath)
	hash, name, ok := strings.Cut(rest, "/")
	if !ok {
		http.NotFound(w, r)
		return
	}

	a, exists := assets[name]
	if !exists {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", a.contentType)
	if hash == a.hash {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(a.body)
}

// Version reports the embedded version of each asset, for `aru doctor` and for
// the debug page.
func Version() string {
	var b strings.Builder
	for _, name := range []string{"app.css", "alpine.min.js", "htmx.min.js"} {
		fmt.Fprintf(&b, "%s %s (%d bytes)\n", name, assets[name].hash, len(assets[name].body))
	}
	return b.String()
}
