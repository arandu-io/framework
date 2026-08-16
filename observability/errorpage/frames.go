// The stack capture, answered by github.com/arandu-io/hesape/exception.

package errorpage

import (
	"github.com/arandu-io/framework/observability"
	"github.com/arandu-io/hesape/exception"
)

// StackFrame is one frame of the stack, already enriched with the source
// snippet around the failing line.
type StackFrame = exception.StackFrame

// Capture collects the stack from skip onwards, marking which frames belong to
// the application: application frames are expanded by default and everything
// else is collapsed.
//
// appModule is the caller's module path.
//
// It is hesape/exception.Capture, and that is the point of it rather than an
// implementation detail. Collapsing by a hard-coded
// github.com/arandu-io/framework would match no hesape frame, so every one of
// them would be called application code, expanded, and its source read off disk
// on every page. The rule hesape applies is its own import path, the standard
// library, and package main -- and, for everything else, the module path the
// application declared.
//
// What that leaves: with appModule empty there is nothing left to tell the
// application from what it imported, and hesape is generous on purpose -- a page
// that collapses the frame somebody is looking for is worse than one that
// expands a frame they are not. So an empty AppModule expands the frames of
// this bridge along with everybody else's. Both skeletons set it, and it is the
// field Options exists for.
//
// The snippet is five lines each side of the failing one: the page is laid out
// for eleven lines.
func Capture(skip int, appModule string) []StackFrame {
	return exception.Capture(skip, appModule)
}

// EditorLink builds the link that opens the file straight in the IDE.
//
// It forwards to observability.EditorLink, which is where the one
// implementation lives: the console needs the same links, and two copies is two
// places to add the next editor. That package is itself a bridge, so the table
// answering is hesape/log's -- see its note on what an unset editor now gets.
func EditorLink(editor, file string, line int) string {
	return observability.EditorLink(editor, file, line)
}
