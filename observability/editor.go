// The editor links, answered by github.com/arandu-io/hesape/log.

package observability

import hlog "github.com/arandu-io/hesape/log"

// EditorLink builds the link that opens a file straight in the IDE, at the line.
//
// It lives here rather than in errorpage because two things need it now -- the
// error page and the console -- and a second copy is a second place to add the
// next editor. The editor name comes from the log configuration.
//
// The scheme has to reach the template as template.URL: html/template rewrites
// an unknown scheme to #ZgotmplZ, which turns every link on the page into a dead
// one and gives no hint why.
//
// Two things changed with the move, and both are visible from here. The table
// is hesape's, and it is longer than the one this package used to carry; an
// unset or unknown editor now gets "" instead of a vscode:// link that opens
// nothing for somebody who configured emacs. And hesape takes an optional path
// rewrite,
// for a link built inside a container that has to open a file outside it; this
// signature has no room for one, so it is the case that needs
// hesape/log.EditorLink directly.
func EditorLink(editor, file string, line int) string {
	return hlog.EditorLink(editor, file, line)
}
