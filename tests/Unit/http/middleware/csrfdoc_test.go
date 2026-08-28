package unit

import (
	"go/ast"
	"go/parser"
	"go/token"
	"html/template"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// documentedPackage is the directory, relative to the module root, whose
// published comments this test reads.
const documentedPackage = "http/middleware"

// action is the delimiter that tells a documented template apart from plain
// markup. A code block without one is HTML, and parsing it as a template would
// assert nothing about it.
const action = "{{"

// TestEveryTemplateDocumentedByTheMiddlewarePackageParses reads the published
// sources of the middleware package, collects every code block in a comment
// that contains a template action, and parses it.
//
// A doc comment is the one part of a package the compiler never reads, so an
// example inside one can be wrong in a way that nothing reports: the tests pass,
// the build is clean, and the first person to find out is whoever copies the
// block into a layout. This test is the compiler that comment blocks do not
// otherwise get.
//
// What counts as a block is the convention a Go doc renderer already uses: a run
// of consecutive comment lines indented past the marker, which is what gets
// shown to a reader as code and therefore what gets copied. A block comment is
// taken whole, because there is no indentation convention inside one to run.
//
// What this does NOT catch, so that the coverage is not read as wider than it
// is:
//
//   - a template action written in prose rather than in an indented block. Prose
//     that discusses the delimiters is not copyable, and may legitimately not
//     parse.
//   - a template held in a string literal in code rather than in a comment.
//     Those are reached by whatever executes them, not by this.
//   - anything a parse cannot see. A snippet naming a field the page data does
//     not have parses, and so does one whose escaping only fails on execution.
//   - comments in test files, which are not published.
//   - any package other than the one named above. The equivalent for another
//     package is this test with that directory, and adding it there is the
//     deliberate way to widen this.
func TestEveryTemplateDocumentedByTheMiddlewarePackageParses(t *testing.T) {
	dir := filepath.Join(moduleRoot(t), documentedPackage)

	found := 0
	for _, path := range publishedSources(t, dir) {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}

		for _, group := range file.Comments {
			for _, block := range templateBlocks(fset, group) {
				found++
				if _, err := template.New(filepath.Base(path)).Parse(block.source); err != nil {
					t.Errorf("%s:%d documents a template that does not parse: %v\n%s\n"+
						"A reader copies a documented block and expects it to run. Fix the "+
						"block, not this test.",
						path, block.number, err, block.source)
				}
			}
		}
	}

	if found == 0 {
		t.Fatalf("no comment in %s documents a template, so this test parsed nothing and "+
			"would pass on anything. If the package no longer documents one, remove this "+
			"test deliberately instead of leaving it here reading nothing.", dir)
	}
}

// commentLine is one line of a comment with its marker removed and its
// indentation kept, paired with the line of the file it came from.
type commentLine struct {
	number int
	text   string
}

// codeBlock is a documented snippet: the joined lines, and the line the run
// starts on.
type codeBlock struct {
	number int
	source string
}

// templateBlocks returns the snippets one comment group documents: every run of
// indented line-comment lines containing a template action, plus any block
// comment containing one.
func templateBlocks(fset *token.FileSet, group *ast.CommentGroup) []codeBlock {
	var blocks []codeBlock
	var run []commentLine

	flush := func() {
		lines := run
		run = nil
		if len(lines) == 0 {
			return
		}
		texts := make([]string, len(lines))
		for i, line := range lines {
			texts[i] = line.text
		}
		source := strings.Join(texts, "\n")
		if strings.Contains(source, action) {
			blocks = append(blocks, codeBlock{number: lines[0].number, source: source})
		}
	}

	for _, comment := range group.List {
		number := fset.Position(comment.Slash).Line

		if !strings.HasPrefix(comment.Text, "//") {
			flush()
			source := strings.TrimSuffix(strings.TrimPrefix(comment.Text, "/*"), "*/")
			if strings.Contains(source, action) {
				blocks = append(blocks, codeBlock{number: number, source: source})
			}
			continue
		}

		// A comment marker is conventionally followed by one space, and only
		// that one is dropped: the indentation past it is what marks a code
		// block and has to survive.
		text := strings.TrimPrefix(comment.Text[len("//"):], " ")
		switch {
		case strings.HasPrefix(text, "\t"), strings.HasPrefix(text, " "):
			run = append(run, commentLine{number: number, text: text})
		case text == "" && len(run) > 0:
			// A blank line inside a code block belongs to it.
			run = append(run, commentLine{number: number, text: text})
		default:
			flush()
		}
	}
	flush()

	return blocks
}

// publishedSources returns the .go files in dir whose comments reach a reader of
// the package documentation: everything that is not a test.
//
// It fails rather than returning nothing, because a check that reads no files
// reports no findings and passes.
func publishedSources(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}

	var paths []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		paths = append(paths, filepath.Join(dir, name))
	}

	if len(paths) == 0 {
		t.Fatalf("no published sources under %s, so this test read nothing and would pass on anything", dir)
	}
	return paths
}

// moduleRoot returns the directory holding the go.mod of the module under test,
// found by walking up from the working directory a test is run in.
func moduleRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above the working directory, so the sources to read cannot be found")
		}
		dir = parent
	}
}
