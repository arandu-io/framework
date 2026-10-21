package feature

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/arandu-io/framework/foundation"
	"github.com/arandu-io/hesape/config"
	"github.com/arandu-io/hesape/database/migrations"
	hfoundation "github.com/arandu-io/hesape/foundation"
	hhttp "github.com/arandu-io/hesape/http"
	"github.com/arandu-io/hesape/routing"
	"github.com/arandu-io/hesape/routing/controllers"
)

// routableModule is written against hesape alone: it implements
// hesape/foundation.Routable, whose router registers controller actions.
type routableModule struct{}

func (routableModule) Name() string { return "notes" }

func (routableModule) Routes(r *controllers.Router) {
	r.Action(http.MethodGet, "/notes", func(ctx *hhttp.Context) error {
		return ctx.Status(http.StatusAccepted)
	}).Name("notes.index")
}

var _ hfoundation.Routable = routableModule{}

// handlerModule is a hesape module that has not moved to Routable: its Routes
// takes the handler-only router. It boots and declares a migration, so the
// test can see that Component hides neither.
type handlerModule struct{ booted *bool }

func (handlerModule) Name() string { return "feed" }

func (handlerModule) Routes(r *routing.Router) {
	r.Get("/feed.xml", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})).Name("feed")
}

func (m handlerModule) Boot(context.Context) error { *m.booted = true; return nil }

func (handlerModule) Migrations() []hfoundation.Migration {
	return []hfoundation.Migration{feedMigration{}}
}

// TestHesapeModulesRegisterAsTheyAre: a Routable module goes to Register as
// it stands, and a module on the handler-only router goes through Component,
// with its routes on the same table and its optional interfaces still seen.
func TestHesapeModulesRegisterAsTheyAre(t *testing.T) {
	booted := false
	app := foundation.New(testConfig(config.EnvProd)).
		Register(routableModule{}, foundation.Component(handlerModule{booted: &booted}))
	if err := app.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}

	for path, want := range map[string]int{"/notes": http.StatusAccepted, "/feed.xml": http.StatusTeapot} {
		rec := httptest.NewRecorder()
		app.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://example.test"+path, nil))
		if rec.Code != want {
			t.Errorf("GET %s = %d, want %d", path, rec.Code, want)
		}
	}

	modules := map[string]string{}
	for _, r := range app.Routes() {
		modules[r.RouteName()] = r.Module
	}
	if modules["notes.index"] != "notes" || modules["feed"] != "feed" {
		t.Errorf("routes are tagged %v, want each with its module's name", modules)
	}

	if !booted {
		t.Error("the wrapped module was not booted: Component hid Bootable")
	}
	found := false
	for _, m := range app.Migrations() {
		found = found || m.GetName() == "2026_10_21_000001_create_feed_table"
	}
	if !found {
		t.Error("the wrapped module's migration was not collected: Component hid Migratable")
	}
}

// feedMigration is the wrapped module's one migration.
type feedMigration struct{}

func (feedMigration) GetName() string                                 { return "2026_10_21_000001_create_feed_table" }
func (feedMigration) Up(context.Context, migrations.Connection) error { return nil }
func (feedMigration) GetConnection() string                           { return "" }
func (feedMigration) ShouldRun() bool                                 { return true }
func (feedMigration) WithinTransaction() bool                         { return true }
