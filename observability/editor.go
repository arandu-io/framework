package observability

import "strconv"

// EditorLink builds the link that opens a file straight in the IDE, at the line.
//
// It lives here rather than in errorpage because two things need it now -- the
// error page and the console -- and a second copy is a second place to add the
// next editor. The editor name comes from config.Config.Editor.
//
// The scheme has to reach the template as template.URL: html/template rewrites
// an unknown scheme to #ZgotmplZ, which turns every link on the page into a dead
// one and gives no hint why.
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
		// VS Code, and whatever forks it next. It is also what an unset editor
		// gets, because the wrong scheme fails visibly and no link fails
		// silently.
		return "vscode://file" + file + ":" + at
	}
}
