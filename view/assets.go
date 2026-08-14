// The served assets, answered by github.com/arandu-io/hesape/view.
//
// The go:embed moved with them: the bytes a browser receives are hesape's, and
// they are byte-identical to the ones this module used to embed. The copy under
// assets/ in this repository is no longer compiled into anything. It is left in
// place deliberately -- THIRD_PARTY.md at the root of this module is the
// copyright notice for exactly those file names, third_party_test.go is what
// keeps that notice from rotting, and neither is inside this package to fix.
// See the report that came with this bridge.
//
// Like the view registry, the asset table is package-level state, and the same
// argument applies: a framework that kept its own would have kyse/fonts
// registering a face into one table while the Handler the module routed served
// the other, which is a 404 on a URL the stylesheet just wrote.

package view

import (
	"net/http"

	hview "github.com/arandu-io/hesape/view"
)

// AssetPath is where assets are served from. The hash is in the path, so the
// response can be cached forever and a new build simply has a new URL.
const AssetPath = hview.AssetPath

// Stylesheet is the name of the one stylesheet, and there is only one.
//
// The collection embeds a default under this name and RegisterStylesheet
// replaces it. Not a second file, not a second URL, not a cascade order: one
// name, one URL, one set of bytes (RULE 9).
const Stylesheet = hview.Stylesheet

// AssetHash is the path segment an asset is served under: the first twelve hex
// characters of the SHA-256 of its bytes.
//
// It is exported because one thing outside this repository has to produce the
// same string. `aru font:add` writes an absolute src: into the stylesheet it
// generates, and it has to name the font's own hash -- a URL carrying any other
// hash is served without caching, by design.
func AssetHash(body []byte) string { return hview.AssetHash(body) }

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
// It replaces rather than adds, and a second registration panics.
func RegisterStylesheet(css []byte) { hview.RegisterStylesheet(css) }

// RegisterAsset adds one file to the served assets.
//
// It is the transport primitive and it knows nothing about what it carries: a
// name, a content type and the bytes. Unlike RegisterStylesheet it ADDS rather
// than replaces, and registering one name twice panics.
func RegisterAsset(name, contentType string, body []byte) {
	hview.RegisterAsset(name, contentType, body)
}

// Assets reports the name, content type and URL of everything served, sorted by
// name.
//
// It exists so a caller can build markup about the assets it registered -- the
// <link rel=preload> for a font, for instance -- without this package having to
// know what a font is.
func Assets() []Asset { return hview.Assets() }

// Asset is one served file, as Assets reports it.
type Asset = hview.Asset

// URL returns the versioned path of an asset: /_arandu/assets/<hash>/htmx.min.js
//
// The hash comes from the content, so upgrading HTMX changes the URL and no
// browser serves a stale script -- without anyone remembering to bump a version.
func URL(name string) string { return hview.URL(name) }

// Handler serves the embedded assets.
//
// Anything whose path carries the right hash is immutable and cached for a year;
// a wrong hash is served without caching, so a stale reference degrades into a
// slow page rather than a broken one.
func Handler(w http.ResponseWriter, r *http.Request) { hview.Handler(w, r) }

// Version reports the served version of each asset, for `aru doctor` and for
// the debug page.
//
// It reports what is served rather than what is embedded, so a stylesheet that
// never reached the browser shows up here as the collection's hash next to a
// project that thought it had built its own.
func Version() string { return hview.Version() }
