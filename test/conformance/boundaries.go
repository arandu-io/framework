package conformance

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// communityAuthModule is the import path native authentication replaced. An
// application that still imports it runs two sign-in stacks.
const communityAuthModule = "github.com/arandu-io/framework/modules/auth"

// copiedFiles are the files the account rules lived in while every
// application kept its own copy of them.
var copiedFiles = []string{
	"app/Services/UserService.go",
	"app/Services/TwoFactorService.go",
	"app/Repositories/TwoFactorRepository.go",
	"app/Models/TwoFactor.go",
	"app/Models/TwoFactorQuery.go",
	"app/Models/RecoveryCodeQuery.go",
	"app/Policies/TwoFactorPolicy.go",
	"app/Events/User.go",
	"database/migrations/20260828_0003_create_two_factor.go",
}

// copiedNames are what the copy declared, anywhere under app/: its services,
// its repository, its models, its policy, and the helpers it hashed recovery
// codes and fingerprinted passwords with.
var copiedNames = map[string]bool{
	"UserService": true, "NewUserService": true, "TwoFactorService": true, "NewTwoFactorService": true,
	"TwoFactorRepository": true, "NewTwoFactorRepository": true, "TwoFactor": true, "RecoveryCode": true,
	"TwoFactorPolicy": true, "HashRecoveryCode": true, "PasswordFingerprint": true,
}

// copiedEvents are the event names the copy declared, checked in app/Events
// only: elsewhere the same word names something else, such as the mail the
// sign-in screens send.
var copiedEvents = map[string]bool{
	"UserRegistered": true, "EmailVerified": true, "PasswordReset": true, "PasswordChanged": true,
	"ProfileUpdated": true, "AccountClosed": true, "TwoFactorEnabled": true, "TwoFactorDisabled": true,
	"RecoveryCodesRegenerated": true, "RecoveryCodeUsed": true,
}

// copiedActions are the user actions account.ActionUser* and
// account.ActionTwoFactor* already declare, checked in app/Policies only: a
// second declaration of the same strings is a second place to change them.
var copiedActions = map[string]bool{
	"ActionUserView": true, "ActionUserCreate": true, "ActionUserUpdate": true,
	"ActionUserDelete": true, "ActionUserNamesPublic": true,
	"ActionTwoFactorRead": true, "ActionTwoFactorManage": true,
}

// Boundaries reads the application at root and checks that native
// authentication stays where it is written once.
//
// root is the application's directory, the one holding go.mod and app/; from
// tests/Conformance it is "../..". The checks:
//
//   - nothing under app, bootstrap, config, database, routes or tests imports
//     the community auth module native authentication replaced;
//   - app/Http/Controllers/Auth/doc.go declares package authui, the package the
//     sign-in screens are published into;
//   - nothing under app, bootstrap or config names the legacy SessionGuard;
//   - the account rules hesape/auth/account owns are not copied back: none of
//     the files the copy lived in exists, no package under app/ declares what
//     it declared, app/Events declares none of its events and app/Policies
//     none of the user actions the account package names.
//
// testdata directories and .kyse.go view sources are not read.
func Boundaries(t *testing.T, root string) {
	t.Helper()
	root = applicationRoot(t, root)
	t.Run("TheCommunityAuthModuleIsNotImported", func(t *testing.T) {
		for _, path := range goFiles(t, root, "app", "bootstrap", "config", "database", "routes", "tests") {
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				t.Errorf("parsing %s: %v", relative(root, path), err)
				continue
			}
			for _, imported := range file.Imports {
				if name, _ := strconv.Unquote(imported.Path.Value); name == communityAuthModule {
					t.Errorf("%s imports the community auth module %s, which native authentication replaced", relative(root, path), communityAuthModule)
				}
			}
		}
	})
	t.Run("TheAuthControllerPlaceholderIsPackageAuthui", func(t *testing.T) {
		path := filepath.Join(root, "app", "Http", "Controllers", "Auth", "doc.go")
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.PackageClauseOnly)
		if err != nil {
			t.Fatalf("parsing the auth controller placeholder: %v", err)
		}
		if file.Name.Name != "authui" {
			t.Fatalf("app/Http/Controllers/Auth/doc.go declares package %s, want authui", file.Name.Name)
		}
	})
	t.Run("NoApplicationCodeNamesSessionGuard", func(t *testing.T) {
		for _, path := range goFiles(t, root, "app", "bootstrap", "config") {
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
			if err != nil {
				t.Errorf("parsing %s: %v", relative(root, path), err)
				continue
			}
			ast.Inspect(file, func(node ast.Node) bool {
				if identifier, ok := node.(*ast.Ident); ok && identifier.Name == "SessionGuard" {
					t.Errorf("%s names the legacy SessionGuard", relative(root, path))
					return false
				}
				return true
			})
		}
	})
	t.Run("TheAccountRulesAreNotCopied", func(t *testing.T) {
		for _, file := range copiedFiles {
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(file))); err == nil {
				t.Errorf("%s is back: the account rules are hesape/auth/account's, built in bootstrap/app.go", file)
			}
		}
		events := filepath.Join(root, "app", "Events")
		policies := filepath.Join(root, "app", "Policies")
		for _, path := range goFiles(t, root, "app") {
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
			if err != nil {
				t.Errorf("parsing %s: %v", relative(root, path), err)
				continue
			}
			directory := filepath.Dir(path)
			for _, declaration := range file.Decls {
				for _, name := range declaredNames(declaration) {
					if copiedNames[name] || copiedEvents[name] && directory == events || copiedActions[name] && directory == policies {
						t.Errorf("%s declares %s, which hesape/auth/account owns", relative(root, path), name)
					}
				}
			}
		}
	})
}

// applicationRoot resolves root and fails unless it is an application: a
// directory with go.mod and app/. A wrong root would otherwise pass every
// check by reading nothing.
func applicationRoot(t *testing.T, root string) string {
	t.Helper()
	absolute, err := filepath.Abs(root)
	if err != nil {
		t.Fatalf("resolving the application root %q: %v", root, err)
	}
	for _, required := range []string{"go.mod", "app"} {
		if _, err := os.Stat(filepath.Join(absolute, required)); err != nil {
			t.Fatalf("%s is not an application root: it has no %s", absolute, required)
		}
	}
	return absolute
}

// goFiles lists the Go sources under the named directories of root that a
// check reads. A directory the application does not have is skipped; testdata,
// vendor and hidden directories are not entered, and a .kyse.go view source,
// which is not Go until it is built, is not listed.
func goFiles(t *testing.T, root string, directories ...string) []string {
	t.Helper()
	var files []string
	for _, directory := range directories {
		start := filepath.Join(root, directory)
		if _, err := os.Stat(start); err != nil {
			continue
		}
		err := filepath.WalkDir(start, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			name := entry.Name()
			if entry.IsDir() {
				if path != start && (name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".")) {
					return filepath.SkipDir
				}
				return nil
			}
			if filepath.Ext(name) == ".go" && !strings.HasSuffix(name, ".kyse.go") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", directory, err)
		}
	}
	return files
}

// declaredNames are the package-level names one declaration introduces: a
// function that is not a method, a type, a constant or a variable.
func declaredNames(declaration ast.Decl) []string {
	var names []string
	switch value := declaration.(type) {
	case *ast.FuncDecl:
		if value.Recv == nil {
			names = append(names, value.Name.Name)
		}
	case *ast.GenDecl:
		for _, spec := range value.Specs {
			switch spec := spec.(type) {
			case *ast.TypeSpec:
				names = append(names, spec.Name.Name)
			case *ast.ValueSpec:
				for _, name := range spec.Names {
					names = append(names, name.Name)
				}
			}
		}
	}
	return names
}

func relative(root, path string) string {
	name, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(name)
}
