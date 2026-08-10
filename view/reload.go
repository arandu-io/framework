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
// The whole signal is which process answers: every process picks a random
// identity at boot, and one different from the identity this page loaded with
// means the code changed.
//
// It ASKS rather than listening, and that is the fix for the defect that sent
// this back to be rewritten -- see the header of framework/kernel/reload.go. A
// held connection spends one of the browser's six per origin and is not
// released when you navigate away, so a handful of clicks froze the site.
//
// Three things make asking cheap enough to be the right answer:
//
//   - It does not ask while you are not looking. document.hidden is true for a
//     background tab, a minimised window and another desktop, and a project
//     left open in a tab for a day would otherwise be eighty thousand requests
//     nobody wanted.
//   - It asks the moment you come back, before the next tick, so returning to
//     the tab is not a wait. That is the case the interval alone gets wrong.
//   - A failed request is the server restarting, which is the most likely
//     moment for one. It is ignored rather than retried faster: the next tick
//     is a second away and the answer will keep until then.
func reloadBody(stream string) []byte {
	return []byte(`(function () {
	var loadedFrom = null;
	var checking = false;

	function check() {
		if (document.hidden || checking) {
			return;
		}
		checking = true;
		fetch(` + quote(stream) + `, {cache: "no-store"})
			.then(function (r) { return r.ok ? r.text() : null; })
			.then(function (id) {
				if (id === null) {
					return;
				}
				id = id.trim();
				if (loadedFrom === null) {
					loadedFrom = id;
				} else if (id !== loadedFrom) {
					location.reload();
				}
			})
			.catch(function () {
				// The server is restarting. Nothing to do: the next tick asks
				// again, and the reload happens when it answers.
			})
			.finally(function () { checking = false; });
	}

	check();
	setInterval(check, 1000);
	document.addEventListener("visibilitychange", check);
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
