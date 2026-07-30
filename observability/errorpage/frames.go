package errorpage

import (
	"os"
	"runtime"
	"strconv"
	"strings"
)

// StackFrame is one frame of the stack, already enriched with the source
// snippet around the failing line.
type StackFrame struct {
	Func    string
	File    string
	Line    int
	IsApp   bool     // false for the runtime, the stdlib and the framework itself
	Snippet []string // surrounding lines, rendered inline
	SnipTop int      // line number of the first snippet line
}

// modulePrefix is the framework's own import path, collapsed by default:
// whoever is debugging wants to see THEIR code, not ours.
const modulePrefix = "github.com/arandu-io/framework"

// Capture collects the stack from skip onwards, marking which frames belong to
// the application. Same decision as Ignition: application frames are expanded
// by default and everything else is collapsed.
//
// appModule is the caller's module path. When empty, every frame that is not
// runtime, stdlib or framework counts as application code.
func Capture(skip int, appModule string) []StackFrame {
	pcs := make([]uintptr, 64)
	n := runtime.Callers(skip, pcs)
	frames := runtime.CallersFrames(pcs[:n])

	var out []StackFrame
	for {
		f, more := frames.Next()

		sf := StackFrame{Func: f.Function, File: f.File, Line: f.Line}
		sf.IsApp = isApp(f.Function, appModule)
		if sf.IsApp {
			sf.Snippet, sf.SnipTop = snippet(f.File, f.Line, 6)
		}
		out = append(out, sf)

		if !more {
			break
		}
	}
	return out
}

func isApp(fn, appModule string) bool {
	switch {
	case appModule != "" && strings.HasPrefix(fn, appModule):
		return true
	case strings.HasPrefix(fn, modulePrefix):
		return false
	case strings.HasPrefix(fn, "runtime."), strings.HasPrefix(fn, "net/http."):
		return false
	}
	return !strings.Contains(fn, "/vendor/")
}

// snippet reads the source around the line. When the binary runs far from its
// sources -- a production container -- it returns nothing and the page degrades
// instead of breaking.
func snippet(file string, line, radius int) ([]string, int) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, 0
	}
	lines := strings.Split(string(b), "\n")
	from := max(0, line-radius-1)
	to := min(len(lines), line+radius)
	return lines[from:to], from + 1
}

// EditorLink builds the link that opens the file straight in the IDE. The
// editor comes from config.Config.Editor.
func EditorLink(editor, file string, line int) string {
	at := strconv.Itoa(line)
	switch editor {
	case "cursor":
		return "cursor://file" + file + ":" + at
	case "goland":
		return "jetbrains://goland/navigate/reference?path=" + file + ":" + at
	case "zed":
		return "zed://file" + file + ":" + at
	default:
		return "vscode://file" + file + ":" + at
	}
}
