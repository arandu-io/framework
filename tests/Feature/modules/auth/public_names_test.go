package feature

import (
	"context"
	"errors"
	"testing"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/modules/auth"
	"github.com/arandu-io/framework/security"
)

func TestPublicNamesAuthorizesADeclaredGuestAndScopesTheQueryToItsTenant(t *testing.T) {
	service, state := serviceOverFakeDB(t)
	state.seedUser(auth.User{
		ID: "user-1", TenantID: "tenant-from-config", Name: "Ada", Email: "ada@example.com",
	})

	names, err := service.PublicNames(
		context.Background(), security.Guest("tenant-from-config"), []string{"user-1"},
	)

	if err != nil {
		t.Fatalf("PublicNames: %v", err)
	}
	if names["user-1"] != "Ada" {
		t.Fatalf("PublicNames returned %v, want user-1 labelled Ada", names)
	}
	args, found := state.argsOf("SELECT id, name, email FROM users")
	if !found {
		t.Fatalf("the authorized projection did not reach the database: %v", state.statements())
	}
	if tenant, _ := args[0].Value.(string); tenant != "tenant-from-config" {
		t.Fatalf("query tenant = %q, want the tenant from the Grant", tenant)
	}
}

func TestPublicNamesAuthorizesASignedInReader(t *testing.T) {
	service, state := serviceOverFakeDB(t)
	state.seedUser(auth.User{
		ID: "user-1", TenantID: "tenant-from-session", Name: "Ada", Email: "ada@example.com",
	})
	reader := security.Subject{ID: "reader-1", Tenant: "tenant-from-session"}

	names, err := service.PublicNames(context.Background(), reader, []string{"user-1"})

	if err != nil {
		t.Fatalf("PublicNames: %v", err)
	}
	if names["user-1"] != "Ada" {
		t.Fatalf("PublicNames returned %v, want user-1 labelled Ada", names)
	}
}

func TestPublicNamesRefusesAnUndeclaredReaderBeforeTheQuery(t *testing.T) {
	service, state := serviceOverFakeDB(t)

	_, err := service.PublicNames(context.Background(), security.Subject{}, []string{"user-1"})

	if !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("PublicNames returned %v, want ErrForbidden", err)
	}
	if got := state.statements(); len(got) != 0 {
		t.Fatalf("an undeclared reader reached the database: %v", got)
	}
}

func TestPublicNamesRefusesAReaderWithoutATenantBeforeTheQuery(t *testing.T) {
	readers := map[string]security.Subject{
		"signed in": {ID: "user-1"},
		"guest":     security.Guest(""),
	}

	for name, reader := range readers {
		t.Run(name, func(t *testing.T) {
			service, state := serviceOverFakeDB(t)

			_, err := service.PublicNames(context.Background(), reader, []string{"user-1"})

			if !errors.Is(err, security.ErrForbidden) {
				t.Fatalf("PublicNames returned %v, want ErrForbidden", err)
			}
			if got := state.statements(); len(got) != 0 {
				t.Fatalf("a reader without a tenant reached the database: %v", got)
			}
		})
	}
}

func TestPublicNamesRefusesACollectionScopeFromAnotherTenant(t *testing.T) {
	reader := security.Guest("tenant-a")

	_, err := security.Authorize(
		context.Background(), auth.UserPolicy{}, reader, auth.ActionUserNamesPublic,
		auth.User{TenantID: "tenant-b"},
	)

	if !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("Authorize returned %v, want ErrForbidden", err)
	}
}

func TestPublicNamesGrantDoesNotOpenFullUserReads(t *testing.T) {
	db, state := newFakeDB()
	t.Cleanup(func() { _ = db.Close() })
	repo := auth.NewUserRepo(data.Wrap(db, data.DialectSQLite))
	grant, err := security.Authorize(
		context.Background(), auth.UserPolicy{}, security.Guest("tenant-a"),
		auth.ActionUserNamesPublic, auth.User{TenantID: "tenant-a"},
	)
	if err != nil {
		t.Fatalf("Authorize public names: %v", err)
	}

	if _, err := repo.Find(context.Background(), grant, "user-1"); !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("Find returned %v, want ErrForbidden", err)
	}
	if _, err := repo.List(context.Background(), grant, data.Query{}); !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("List returned %v, want ErrForbidden", err)
	}
	if got := state.statements(); len(got) != 0 {
		t.Fatalf("a public names Grant reached a full user query: %v", got)
	}
}

func TestFullUserViewGrantDoesNotOpenThePublicNamesProjection(t *testing.T) {
	db, state := newFakeDB()
	t.Cleanup(func() { _ = db.Close() })
	repo := auth.NewUserRepo(data.Wrap(db, data.DialectSQLite))
	reader := security.Subject{ID: "admin-1", Tenant: "tenant-a", Roles: []string{"admin"}}
	grant, err := security.Authorize(
		context.Background(), auth.UserPolicy{}, reader, auth.ActionUserView,
		auth.User{ID: "user-1", TenantID: "tenant-a"},
	)
	if err != nil {
		t.Fatalf("Authorize full user view: %v", err)
	}

	_, err = repo.NamesByID(context.Background(), grant, []string{"user-1"})

	if !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("NamesByID returned %v, want ErrForbidden", err)
	}
	if got := state.statements(); len(got) != 0 {
		t.Fatalf("a full user Grant reached the public names query: %v", got)
	}
}

func TestMakingNamesPublicDoesNotMakeAUserPublic(t *testing.T) {
	reader := security.Guest("tenant-a")

	_, err := security.Authorize(
		context.Background(), auth.UserPolicy{}, reader, auth.ActionUserView,
		auth.User{ID: "user-1", TenantID: "tenant-a"},
	)

	if !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("Authorize returned %v, want ErrForbidden", err)
	}
}
