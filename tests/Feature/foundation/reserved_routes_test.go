package feature

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/arandu-io/framework/foundation"
	fhttp "github.com/arandu-io/framework/http"
	testbase "github.com/arandu-io/framework/tests"
	"github.com/arandu-io/hesape/config"
)

// reservedRouteModule represents application code trying to claim the
// framework's private HTTP namespace.
type reservedRouteModule struct{}

func (*reservedRouteModule) Name() string { return "billing" }

func (*reservedRouteModule) Routes(r *fhttp.Router) {
	r.Get("/_arandu/export", func(http.ResponseWriter, *http.Request) {})
}

func TestBootRejectsApplicationRoutesUnderTheReservedPrefix(t *testing.T) {
	k := foundation.New(testConfig(config.EnvProd)).Register(&reservedRouteModule{})

	err := k.Boot(context.Background())
	if err == nil {
		t.Fatal("an application route under /_arandu/ was accepted")
	}
	const want = `arandu: module "billing" registered route "/_arandu/export" under the reserved /_arandu/ namespace`
	if err.Error() != want {
		t.Fatalf("Boot error = %q, want %q", err, want)
	}
}

// TestBootRejectsReservedRoutesSmuggledThroughAnEmbeddedFirstPartyModule runs
// the probe from another module so the dynamic type has an external package
// path. A local test type would itself belong to this module and prove nothing.
func TestBootRejectsReservedRoutesSmuggledThroughAnEmbeddedFirstPartyModule(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles an external module with the go tool")
	}

	root := testbase.ModuleRoot(t)
	dir := t.TempDir()
	goMod := "module example.test/reserved-route-smuggling\n\n" +
		"go 1.26\n\n" +
		"require github.com/arandu-io/framework v0.0.0\n\n" +
		"replace github.com/arandu-io/framework => " + filepath.ToSlash(root) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o600); err != nil {
		t.Fatalf("write external go.mod: %v", err)
	}
	goSum, err := os.ReadFile(filepath.Join(root, "go.sum"))
	if err != nil {
		t.Fatalf("read framework go.sum: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.sum"), goSum, 0o600); err != nil {
		t.Fatalf("write external go.sum: %v", err)
	}

	const source = `package main

import (
	"context"
	"net/http"

	"github.com/arandu-io/framework/foundation"
	"github.com/arandu-io/framework/foundation/bootstrap"
	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/view"
	"github.com/arandu-io/hesape/config"
	"github.com/arandu-io/hesape/encryption"
)

type smuggledModule struct{ view.Module }

func (*smuggledModule) Name() string { return "billing" }

func (*smuggledModule) Routes(r *fhttp.Router) {
	r.Get("/_arandu/export", func(http.ResponseWriter, *http.Request) {})
}

func main() {
	cfg := bootstrap.Configuration{App: config.App{
		Env: config.EnvProd,
		Key: make([]byte, encryption.KeySize),
	}}
	err := foundation.New(cfg).Register(&smuggledModule{}).Boot(context.Background())
	if err == nil {
		panic("a reserved route was accepted through an embedded view.Module")
	}
	const want = "arandu: module \"billing\" registered route \"/_arandu/export\" under the reserved /_arandu/ namespace"
	if err.Error() != want {
		panic(err)
	}
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(source), 0o600); err != nil {
		t.Fatalf("write external probe: %v", err)
	}

	cmd := exec.Command("go", "run", "-mod=mod", ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("external reserved-route probe failed: %v\n%s", err, out)
	}
}
