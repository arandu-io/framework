package unit

import (
	"go/ast"
	"go/doc"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	testbase "github.com/arandu-io/framework/tests"
)

// replacement is what every deprecation notice in the package names, so the
// editor that strikes a call through also says where it went.
const replacement = "github.com/arandu-io/hesape/test"

// namesReplacement matches the replacement as a whole import path, so a notice
// naming hesape/testing or the old hesape/arandutest does not pass for it.
var namesReplacement = regexp.MustCompile(regexp.QuoteMeta(replacement) + `($|[^\w/])`)

// deprecation is the paragraph of a doc comment that starts with
// "Deprecated:", the form gopls, staticcheck and pkg.go.dev read, or empty
// when there is none.
func deprecation(comment string) string {
	for _, paragraph := range strings.Split(comment, "\n\n") {
		if strings.HasPrefix(paragraph, "Deprecated: ") {
			return paragraph
		}
	}
	return ""
}

// The package is removed in v1.0.0. Saying so in prose reaches whoever reads
// the package documentation; a Deprecated paragraph on every exported name is
// what reaches the caller, at the call, in the editor and in the linter.
func TestEveryExportOfTheBridgeIsDeprecatedTowardHesape(t *testing.T) {
	dir := filepath.Join(testbase.ModuleRoot(t), "arandutest")
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parsing %s: %v", dir, err)
	}
	pkg, ok := pkgs["arandutest"]
	if !ok {
		t.Fatalf("%s holds no package arandutest", dir)
	}

	var files []*ast.File
	for _, f := range pkg.Files {
		files = append(files, f)
	}
	p, err := doc.NewFromFiles(fset, files, "github.com/arandu-io/framework/arandutest", doc.PreserveAST)
	if err != nil {
		t.Fatalf("reading the documentation: %v", err)
	}

	checked := map[string]string{"package arandutest": p.Doc}
	for _, f := range p.Funcs {
		checked["func "+f.Name] = f.Doc
	}
	for _, ty := range p.Types {
		checked["type "+ty.Name] = ty.Doc
		for _, f := range ty.Funcs {
			checked["func "+f.Name] = f.Doc
		}
		for _, m := range ty.Methods {
			checked["method "+ty.Name+"."+m.Name] = m.Doc
		}
	}
	for _, v := range append(p.Consts, p.Vars...) {
		for _, name := range v.Names {
			if ast.IsExported(name) {
				checked["value "+name] = v.Doc
			}
		}
	}

	// The count keeps the walk honest: a parse that found nothing would pass
	// every check below.
	if len(checked) < 15 {
		t.Fatalf("found %d documented names in %s, want the package and its fourteen exports at least", len(checked), dir)
	}
	for name, comment := range checked {
		notice := deprecation(comment)
		switch {
		case notice == "":
			t.Errorf("%s carries no Deprecated: paragraph", name)
		case !namesReplacement.MatchString(notice):
			t.Errorf("%s is deprecated without naming %s:\n%s", name, replacement, notice)
		}
	}
}
