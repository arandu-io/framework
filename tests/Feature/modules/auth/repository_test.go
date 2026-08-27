package feature

import (
	"context"
	"errors"
	"testing"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/modules/auth"
	"github.com/arandu-io/framework/security"
)

var errRowsAffectedUnavailable = errors.New("test driver cannot report affected rows")

func TestUpdatePropagatesARowsAffectedFailure(t *testing.T) {
	db, state := newFakeDB()
	t.Cleanup(func() { _ = db.Close() })
	state.rowsAffectedFails(errRowsAffectedUnavailable)
	repo := auth.NewUserRepo(data.Wrap(db, data.DialectSQLite))
	grant := security.SystemGrant(auth.ActionUserUpdate, "tenant-from-grant")

	_, err := repo.Update(context.Background(), grant, auth.User{
		ID: "user-1", TenantID: "tenant-from-input", Email: "user@example.com",
	})

	if !errors.Is(err, errRowsAffectedUnavailable) {
		t.Fatalf("Update returned %v, want the driver's RowsAffected failure", err)
	}
}

func TestUpdateReturnsTheTenantFromTheGrant(t *testing.T) {
	db, _ := newFakeDB()
	t.Cleanup(func() { _ = db.Close() })
	repo := auth.NewUserRepo(data.Wrap(db, data.DialectSQLite))
	grant := security.SystemGrant(auth.ActionUserUpdate, "tenant-from-grant")

	updated, err := repo.Update(context.Background(), grant, auth.User{
		ID: "user-1", TenantID: "tenant-from-input", Email: "user@example.com",
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if updated.TenantID != "tenant-from-grant" {
		t.Fatalf("Update returned tenant %q from its argument, want tenant %q from the Grant", updated.TenantID, "tenant-from-grant")
	}
}

func TestSetPasswordPropagatesARowsAffectedFailure(t *testing.T) {
	db, state := newFakeDB()
	t.Cleanup(func() { _ = db.Close() })
	state.rowsAffectedFails(errRowsAffectedUnavailable)
	repo := auth.NewUserRepo(data.Wrap(db, data.DialectSQLite))
	grant := security.SystemGrant(auth.ActionUserUpdate, "tenant-from-grant")

	err := repo.SetPassword(context.Background(), grant, "user-1", "a-password-hash")

	if !errors.Is(err, errRowsAffectedUnavailable) {
		t.Fatalf("SetPassword returned %v, want the driver's RowsAffected failure", err)
	}
}

func TestDeletePropagatesARowsAffectedFailure(t *testing.T) {
	db, state := newFakeDB()
	t.Cleanup(func() { _ = db.Close() })
	state.rowsAffectedFails(errRowsAffectedUnavailable)
	repo := auth.NewUserRepo(data.Wrap(db, data.DialectSQLite))
	grant := security.SystemGrant(auth.ActionUserDelete, "tenant-from-grant")

	err := repo.Delete(context.Background(), grant, "user-1")

	if !errors.Is(err, errRowsAffectedUnavailable) {
		t.Fatalf("Delete returned %v, want the driver's RowsAffected failure", err)
	}
}

func TestUpdateReturnsErrUserNotFoundWhenNoRowsChange(t *testing.T) {
	db, state := newFakeDB()
	t.Cleanup(func() { _ = db.Close() })
	state.reportsRowsAffected(0)
	repo := auth.NewUserRepo(data.Wrap(db, data.DialectSQLite))
	grant := security.SystemGrant(auth.ActionUserUpdate, "tenant-from-grant")

	_, err := repo.Update(context.Background(), grant, auth.User{ID: "missing", Email: "user@example.com"})

	if !errors.Is(err, auth.ErrUserNotFound) {
		t.Fatalf("Update returned %v, want ErrUserNotFound", err)
	}
}

func TestSetPasswordReturnsErrUserNotFoundWhenNoRowsChange(t *testing.T) {
	db, _ := newFakeDB()
	t.Cleanup(func() { _ = db.Close() })
	repo := auth.NewUserRepo(data.Wrap(db, data.DialectSQLite))
	grant := security.SystemGrant(auth.ActionUserUpdate, "tenant-from-grant")

	err := repo.SetPassword(context.Background(), grant, "missing", "a-password-hash")

	if !errors.Is(err, auth.ErrUserNotFound) {
		t.Fatalf("SetPassword returned %v, want ErrUserNotFound", err)
	}
}

func TestDeleteReturnsErrUserNotFoundWhenNoRowsChange(t *testing.T) {
	db, state := newFakeDB()
	t.Cleanup(func() { _ = db.Close() })
	state.reportsRowsAffected(0)
	repo := auth.NewUserRepo(data.Wrap(db, data.DialectSQLite))
	grant := security.SystemGrant(auth.ActionUserDelete, "tenant-from-grant")

	err := repo.Delete(context.Background(), grant, "missing")

	if !errors.Is(err, auth.ErrUserNotFound) {
		t.Fatalf("Delete returned %v, want ErrUserNotFound", err)
	}
}
