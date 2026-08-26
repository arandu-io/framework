package unit

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	testbase "github.com/arandu-io/framework/tests"
)

const (
	bridgeMarker = "This package is a bridge. It is removed in v1.0.0;"
	hesapePrefix = "github.com/arandu-io/hesape"
)

type bridgeSurface map[string][]string

type sourceInventory struct {
	bridges       bridgeSurface
	hesapeImports bridgeSurface
}

func inventoryItems(raw string) []string {
	return strings.Fields(raw)
}

func TestHistoricalBridgeSurfaceIsFrozen(t *testing.T) {
	got := inspectFrameworkSource(t, testbase.ModuleRoot(t)).bridges
	drift := bridgeSurfaceDrift(historicalBridgeSurface, got, "bridge package", "export")
	if len(drift) == 0 {
		return
	}

	t.Fatalf("historical bridge surface changed:\n  %s\nbridges are compatibility-only and are removed in v1.0.0; add new API to Hesape instead", strings.Join(drift, "\n  "))
}

func TestDirectHesapeDependenciesAreFrozen(t *testing.T) {
	got := inspectFrameworkSource(t, testbase.ModuleRoot(t)).hesapeImports
	drift := bridgeSurfaceDrift(allowedHesapeImports, got, "framework package", "direct Hesape import")
	if len(drift) == 0 {
		return
	}

	t.Fatalf("framework-to-Hesape dependency inventory changed:\n  %s\nkeep the boundary explicit; a new bridge or dependency must not enter through an incidental import", strings.Join(drift, "\n  "))
}

func TestBridgeInventoryRejectsAnUnexpectedExport(t *testing.T) {
	want := bridgeSurface{"data": {"type.DB"}}
	got := bridgeSurface{"data": {"type.DB", "func.NewShortcut"}}

	drift := strings.Join(bridgeSurfaceDrift(want, got, "bridge package", "export"), "\n")
	if !strings.Contains(drift, "data: unexpected export func.NewShortcut") {
		t.Fatalf("unexpected export was accepted; drift was %q", drift)
	}
}

func TestSourceInventoryFindsAControlledBridge(t *testing.T) {
	root := t.TempDir()
	source := `// Package probe is a compatibility probe.
//
// This package is a bridge. It is removed in v1.0.0; import the target directly.
package probe

import "github.com/arandu-io/hesape/database"

type Legacy = database.DB

func NewShortcut() {}
`
	if err := os.WriteFile(filepath.Join(root, "probe.go"), []byte(source), 0o600); err != nil {
		t.Fatalf("write controlled bridge: %v", err)
	}

	got := inspectFrameworkSource(t, root)
	wantBridges := bridgeSurface{".": {"func.NewShortcut", "type.Legacy"}}
	wantImports := bridgeSurface{".": {"github.com/arandu-io/hesape/database"}}
	if drift := bridgeSurfaceDrift(wantBridges, got.bridges, "bridge package", "export"); len(drift) > 0 {
		t.Fatalf("controlled bridge was not inventoried:\n  %s", strings.Join(drift, "\n  "))
	}
	if drift := bridgeSurfaceDrift(wantImports, got.hesapeImports, "framework package", "direct Hesape import"); len(drift) > 0 {
		t.Fatalf("controlled dependency was not inventoried:\n  %s", strings.Join(drift, "\n  "))
	}
}

func inspectFrameworkSource(t *testing.T, root string) sourceInventory {
	t.Helper()

	type packageSource struct {
		bridge  bool
		exports map[string]struct{}
		imports map[string]struct{}
	}

	packages := make(map[string]*packageSource)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && (entry.Name() == ".git" || entry.Name() == "testdata" || entry.Name() == "tests") {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, ".kyse.go") {
			return nil
		}

		relDir, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		relDir = filepath.ToSlash(relDir)

		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
		if err != nil {
			return err
		}

		pkg := packages[relDir]
		if pkg == nil {
			pkg = &packageSource{exports: make(map[string]struct{}), imports: make(map[string]struct{})}
			packages[relDir] = pkg
		}
		if file.Doc != nil && strings.Contains(file.Doc.Text(), bridgeMarker) {
			pkg.bridge = true
		}

		for _, importSpec := range file.Imports {
			importPath, err := strconv.Unquote(importSpec.Path.Value)
			if err != nil {
				return err
			}
			if importPath == hesapePrefix || strings.HasPrefix(importPath, hesapePrefix+"/") {
				pkg.imports[importPath] = struct{}{}
			}
		}
		collectExportedDeclarations(file, pkg.exports)
		return nil
	})
	if err != nil {
		t.Fatalf("inspect framework source: %v", err)
	}

	result := sourceInventory{bridges: make(bridgeSurface), hesapeImports: make(bridgeSurface)}
	for path, pkg := range packages {
		if pkg.bridge {
			result.bridges[path] = sortedSet(pkg.exports)
		}
		if len(pkg.imports) > 0 {
			result.hesapeImports[path] = sortedSet(pkg.imports)
		}
	}
	return result
}

func collectExportedDeclarations(file *ast.File, exports map[string]struct{}) {
	for _, declaration := range file.Decls {
		switch declaration := declaration.(type) {
		case *ast.GenDecl:
			for _, spec := range declaration.Specs {
				switch spec := spec.(type) {
				case *ast.TypeSpec:
					if spec.Name.IsExported() {
						exports["type."+spec.Name.Name] = struct{}{}
					}
				case *ast.ValueSpec:
					for _, name := range spec.Names {
						if name.IsExported() {
							exports[declaration.Tok.String()+"."+name.Name] = struct{}{}
						}
					}
				}
			}
		case *ast.FuncDecl:
			if !declaration.Name.IsExported() {
				continue
			}
			if declaration.Recv == nil {
				exports["func."+declaration.Name.Name] = struct{}{}
				continue
			}
			receiver := receiverName(declaration.Recv.List[0].Type)
			if ast.IsExported(receiver) {
				exports["method."+receiver+"."+declaration.Name.Name] = struct{}{}
			}
		}
	}
}

func receiverName(expression ast.Expr) string {
	switch expression := expression.(type) {
	case *ast.Ident:
		return expression.Name
	case *ast.StarExpr:
		return receiverName(expression.X)
	case *ast.IndexExpr:
		return receiverName(expression.X)
	case *ast.IndexListExpr:
		return receiverName(expression.X)
	default:
		return "<unknown>"
	}
}

func sortedSet(set map[string]struct{}) []string {
	items := make([]string, 0, len(set))
	for item := range set {
		items = append(items, item)
	}
	sort.Strings(items)
	return items
}

func bridgeSurfaceDrift(want, got bridgeSurface, packageKind, itemKind string) []string {
	keys := make(map[string]struct{}, len(want)+len(got))
	for path := range want {
		keys[path] = struct{}{}
	}
	for path := range got {
		keys[path] = struct{}{}
	}

	paths := sortedSet(keys)
	var drift []string
	for _, path := range paths {
		wantItems, expected := want[path]
		gotItems, present := got[path]
		if !expected {
			drift = append(drift, "unexpected "+packageKind+" "+path)
			for _, item := range gotItems {
				drift = append(drift, path+": unexpected "+itemKind+" "+item)
			}
			continue
		}
		if !present {
			drift = append(drift, "missing "+packageKind+" "+path)
			continue
		}

		wantSet := make(map[string]struct{}, len(wantItems))
		gotSet := make(map[string]struct{}, len(gotItems))
		for _, item := range wantItems {
			wantSet[item] = struct{}{}
		}
		for _, item := range gotItems {
			gotSet[item] = struct{}{}
		}
		for _, item := range wantItems {
			if _, ok := gotSet[item]; !ok {
				drift = append(drift, path+": missing "+itemKind+" "+item)
			}
		}
		for _, item := range gotItems {
			if _, ok := wantSet[item]; !ok {
				drift = append(drift, path+": unexpected "+itemKind+" "+item)
			}
		}
	}
	return drift
}
