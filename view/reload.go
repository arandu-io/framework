package view

import "sync"

// The development live-reload script.
//
// It lives here and not in the kernel because it is an asset and a tag, which is
// what this package is for -- and because the kernel cannot reach this package
// at all: view imports kernel to be a Module, so the arrow only points one way.
// The kernel asks for it through an optional interface and supplies the address
// of the stream, which is the one thing it owns.
//
// Why it is a file rather than an inline <script>: the CSP is script-src 'self'
// (RULE 13). An inline tag is refused by it -- silently, which would read as the
// feature simply not working -- so this is registered like every other asset and
// referenced by its content-addressed URL.

// reloadAsset is the file name the script is served under.
const reloadAsset = "arandu-reload.js"

var reloadOnce sync.Once
var reloadTag string

// ReloadTag registers the reload script and returns the tag that runs it.
//
// stream is where the script listens, and it comes from the caller because the
// route belongs to the kernel: two constants for one address is how a client and
// a server come to disagree about it.
//
// Registering happens once. A second call returns the same tag rather than
// panicking on a duplicate asset, so a test that boots two kernels is not a
// crash.
func ReloadTag(stream string) string {
	reloadOnce.Do(func() {
		RegisterAsset(reloadAsset, "text/javascript; charset=utf-8", reloadBody(stream))
		for _, a := range Assets() {
			if a.Name == reloadAsset {
				reloadTag = `<script src="` + a.URL + `" defer></script>`
			}
		}
	})
	return reloadTag
}

// reloadBody is the script.
//
// The whole signal is which process answers the stream: EventSource reconnects
// on its own after a restart, and an identity different from the one this page
// loaded with means the code changed. A reconnect to the same process -- a
// dropped network, a sleeping laptop -- reports the same identity and reloads
// nothing.
func reloadBody(stream string) []byte {
	return []byte(`(function () {
	var loadedFrom = null;
	var source = new EventSource(` + quote(stream) + `);
	source.onmessage = function (event) {
		if (loadedFrom === null) {
			loadedFrom = event.data;
			return;
		}
		if (event.data !== loadedFrom) {
			source.close();
			location.reload();
		}
	};
})();
`)
}

// quote writes a JavaScript string literal. The address is a path this binary
// chose, but building markup by concatenation is how the one case that is not
// gets through.
func quote(s string) string {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '"')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '"', '\\':
			out = append(out, '\\', c)
		case '<', '>', '&':
			// Escaped as hex, so the literal cannot close the script element it
			// is served in -- this file is served on its own today, and a tag
			// that inlined it later would otherwise be an injection point.
			const hex = "0123456789abcdef"
			out = append(out, '\\', 'x', hex[c>>4], hex[c&0xf])
		default:
			out = append(out, c)
		}
	}
	return string(append(out, '"'))
}
