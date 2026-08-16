// The development live-reload script, answered by
// github.com/arandu-io/hesape/view.
//
// It bridges rather than staying because it registers itself through
// RegisterAsset: the script has to land in the same asset table the Handler
// above serves out of, and that table is hesape's now.

package view

import hview "github.com/arandu-io/hesape/view"

// ReloadTag registers the reload script and returns the tag that runs it.
//
// stream is where the script listens, and it comes from the caller because the
// route belongs to the kernel: two constants for one address is how a client and
// a server come to disagree about it.
//
// Registering happens once. A second call returns the same tag rather than
// panicking on a duplicate asset, so a test that boots two kernels is not a
// crash.
//
// Why it is a file rather than an inline <script>: the CSP is script-src 'self',
// and an inline tag is refused by it -- silently, which would read as the
// feature simply not working -- so this is registered like every other asset and
// referenced by its content-addressed URL.
func ReloadTag(stream string) string { return hview.ReloadTag(stream) }
