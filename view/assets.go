// Package view is the view layer: kyse for markup, HTMX for interaction,
// Alpine for ephemeral client state, Tailwind for style. It is a binary and it
// is never Node.
//
// A project that uses it still runs with `git clone && aru dev`: no
// node_modules, no package.json, no lockfile of JavaScript, nothing installed
// beyond Go and the standalone binaries the CLI fetches. Having a build step is
// allowed; being Node is not (RULE 13).
//
// It lives in the framework and the views do not, and that split is deliberate:
// resources/views/ belongs to the project, because it is edited; the rendering
// machinery belongs here, because it is not. It used to be a repository of its
// own, and dissolving it is ADR 0021.
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
	"sync"
)

// The scripts are embedded, not fetched.
//
// This is not preference. The SecurityHeaders middleware sets
// "script-src 'self'", so loading HTMX from a CDN would mean loosening the CSP
// -- paying in security for convenience. It also means the deploy stays one
// binary: no asset publishing step, no CDN to invalidate, no storage:link.
//
//go:embed assets/htmx.min.js assets/alpine.min.js assets/basecoat.bundle.js assets/theme.js assets/app.css
var files embed.FS

// AssetPath is where assets are served from. The hash is in the path, so the
// response can be cached forever and a new build simply has a new URL.
const AssetPath = "/_arandu/assets/"

// Stylesheet is the name of the one stylesheet, and there is only one.
//
// The framework embeds a default under this name and RegisterStylesheet
// replaces it. Not a second file, not a second URL, not a cascade order: one
// name, one URL, one set of bytes (RULE 9).
const Stylesheet = "app.css"

const stylesheetType = "text/css; charset=utf-8"

// asset is one embedded file with its content hash.
type asset struct {
	name        string
	contentType string
	body        []byte
	hash        string
}

// assetsMu guards the table, which RegisterStylesheet writes to.
//
// The write happens in init(), before anything serves, so the lock buys nothing
// at runtime and costs nothing either. It is here so that a test replacing the
// stylesheet is not a data race against a server it started.
var (
	assetsMu sync.RWMutex
	assets   = map[string]*asset{}

	// appStylesheet records that the application already replaced the default,
	// so a second replacement is an error rather than a coin toss.
	appStylesheet bool
)

func init() {
	for name, contentType := range map[string]string{
		"htmx.min.js":        "application/javascript; charset=utf-8",
		"alpine.min.js":      "application/javascript; charset=utf-8",
		"basecoat.bundle.js": "application/javascript; charset=utf-8",
		"theme.js":           "application/javascript; charset=utf-8",
		Stylesheet:           stylesheetType,
	} {
		body, err := files.ReadFile("assets/" + name)
		if err != nil {
			// The file is embedded at build time: if it is missing, the binary is
			// broken and there is nothing to recover from at runtime.
			panic("view: missing embedded asset " + name + ": " + err.Error())
		}
		assets[name] = newAsset(name, contentType, body)
	}
}

// newAsset hashes the body and returns the servable asset.
//
// The hash is computed here and nowhere else, which is what keeps the URL and
// the bytes from ever disagreeing: replacing the body without rehashing would
// leave every browser holding the previous stylesheet at the same URL, forever,
// because that URL is served with max-age=31536000, immutable.
func newAsset(name, contentType string, body []byte) *asset {
	sum := sha256.Sum256(body)
	return &asset{
		name:        name,
		contentType: contentType,
		body:        body,
		hash:        hex.EncodeToString(sum[:])[:12],
	}
}

// RegisterStylesheet replaces the embedded stylesheet with the application's.
//
// `aru view:build` compiles resources/css/app.css into assets/app.css, and the
// skeleton hands those bytes over from init(), the same shape as Register:
//
//	//go:embed assets/app.css
//	var appCSS []byte
//
//	func init() { view.RegisterStylesheet(appCSS) }
//
// It replaces rather than adds. The framework's copy is a default so that a
// project renders before its first view:build, not a base layer to cascade on
// top of -- two stylesheets would mean two URLs, an order that matters, and a
// specificity fight nobody can win from the application side.
//
// Without it the browser received the framework's stylesheet, md5 identical,
// and every class written in a project's own views did nothing. Nothing failed:
// the page was served, with 200, unstyled.
//
// Registering twice panics rather than replacing, for the same reason Register
// does: two stylesheets for one name is a build artifact that outlived its
// source, and finding out at boot beats finding out from a page that renders
// with somebody else's design.
func RegisterStylesheet(css []byte) {
	if len(css) == 0 {
		panic("view: RegisterStylesheet was given an empty stylesheet -- run `aru view:build`")
	}

	assetsMu.Lock()
	defer assetsMu.Unlock()

	if appStylesheet {
		panic("view: the application stylesheet is already registered -- a stale generated file is probably still on disk")
	}
	appStylesheet = true
	assets[Stylesheet] = newAsset(Stylesheet, stylesheetType, css)
}

// URL returns the versioned path of an asset: /_arandu/assets/<hash>/htmx.min.js
//
// The hash comes from the content, so upgrading HTMX changes the URL and no
// browser serves a stale script -- without anyone remembering to bump a version.
func URL(name string) string {
	assetsMu.RLock()
	defer assetsMu.RUnlock()

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

	assetsMu.RLock()
	a, exists := assets[name]
	assetsMu.RUnlock()
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

// Version reports the served version of each asset, for `aru doctor` and for
// the debug page.
//
// It reports what is served rather than what is embedded, so a stylesheet that
// never reached the browser shows up here as the framework's hash next to a
// project that thought it had built its own.
func Version() string {
	assetsMu.RLock()
	defer assetsMu.RUnlock()

	var b strings.Builder
	for _, name := range []string{Stylesheet, "alpine.min.js", "htmx.min.js"} {
		fmt.Fprintf(&b, "%s %s (%d bytes)\n", name, assets[name].hash, len(assets[name].body))
	}
	return b.String()
}
