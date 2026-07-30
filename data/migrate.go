package data

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// MigrationsTable is where applied migration ids are recorded.
const MigrationsTable = "arandu_migrations"

// Migration is a versioned, immutable-once-published schema change.
//
// The id carries its own order -- "2026_07_29_000001_create_users_table" -- for
// the same reason Laravel names its files that way: a migration that sorts
// differently on two machines applies in a different order on two machines.
type Migration struct {
	ID   string
	Up   string
	Down string
}

// AppliedMigration is one row of the tracking table.
type AppliedMigration struct {
	ID        string
	Batch     int
	AppliedAt time.Time
}

// Migrate applies the pending migrations, in the given order, and returns the
// ids it applied.
//
// Everything applied by one call shares a batch number, which is what makes
// Rollback undo a deploy rather than a single migration -- the same model
// Laravel uses, and the reason its rollback is useful in practice.
//
// Each migration runs inside its own transaction together with the insert into
// the tracking table, so a failure halfway cannot leave the schema ahead of the
// record. It stops at the first failure: applying later migrations over a broken
// schema turns one clear error into an unrecoverable database.
func Migrate(ctx context.Context, db *DB, migrations []Migration) ([]string, error) {
	pending, err := Pending(ctx, db, migrations)
	if err != nil {
		return nil, err
	}
	if len(pending) == 0 {
		return nil, nil
	}

	batch, err := nextBatch(ctx, db)
	if err != nil {
		return nil, err
	}

	var applied []string
	for _, m := range pending {
		if m.ID == "" {
			return applied, fmt.Errorf("arandu: migration with empty id")
		}
		if err := applyOne(ctx, db, m, batch); err != nil {
			return applied, err
		}
		applied = append(applied, m.ID)
	}
	return applied, nil
}

// Rollback undoes the last batch and returns the ids it reverted, most recent
// first.
//
// A migration with an empty Down is refused rather than skipped: silently
// leaving part of a batch in place is how a rollback produces a schema that
// matches neither version.
func Rollback(ctx context.Context, db *DB, migrations []Migration) ([]string, error) {
	applied, err := AppliedMigrations(ctx, db)
	if err != nil {
		return nil, err
	}
	if len(applied) == 0 {
		return nil, nil
	}

	last := 0
	for _, a := range applied {
		if a.Batch > last {
			last = a.Batch
		}
	}

	byID := make(map[string]Migration, len(migrations))
	for _, m := range migrations {
		byID[m.ID] = m
	}

	// Reverse order: the last thing applied is the first thing undone.
	var batch []AppliedMigration
	for i := len(applied) - 1; i >= 0; i-- {
		if applied[i].Batch == last {
			batch = append(batch, applied[i])
		}
	}

	var reverted []string
	for _, a := range batch {
		m, known := byID[a.ID]
		if !known {
			return reverted, fmt.Errorf("arandu: migration %s is recorded but no longer declared by any module", a.ID)
		}
		if strings.TrimSpace(m.Down) == "" {
			return reverted, fmt.Errorf("arandu: migration %s has no Down and cannot be rolled back", a.ID)
		}
		if err := revertOne(ctx, db, m); err != nil {
			return reverted, err
		}
		reverted = append(reverted, a.ID)
	}
	return reverted, nil
}

// Status returns every declared migration with the batch it was applied in, or
// zero when it is still pending. It is what `aru migrate:status` prints.
func Status(ctx context.Context, db *DB, migrations []Migration) ([]AppliedMigration, error) {
	applied, err := AppliedMigrations(ctx, db)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]AppliedMigration, len(applied))
	for _, a := range applied {
		byID[a.ID] = a
	}

	out := make([]AppliedMigration, 0, len(migrations))
	for _, m := range migrations {
		if a, ok := byID[m.ID]; ok {
			out = append(out, a)
			continue
		}
		out = append(out, AppliedMigration{ID: m.ID})
	}
	return out, nil
}

// AppliedMigrations returns the tracking table, ordered by batch and id.
func AppliedMigrations(ctx context.Context, db *DB) ([]AppliedMigration, error) {
	if err := ensureMigrationsTable(ctx, db); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx,
		`SELECT id, batch, applied_at FROM `+MigrationsTable+` ORDER BY batch, id`)
	if err != nil {
		return nil, fmt.Errorf("arandu: reading %s: %w", MigrationsTable, err)
	}
	defer rows.Close()

	var out []AppliedMigration
	for rows.Next() {
		var a AppliedMigration
		if err := rows.Scan(&a.ID, &a.Batch, &a.AppliedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Pending returns the migrations that have not been applied yet, in order.
func Pending(ctx context.Context, db *DB, migrations []Migration) ([]Migration, error) {
	applied, err := AppliedMigrations(ctx, db)
	if err != nil {
		return nil, err
	}
	done := make(map[string]bool, len(applied))
	for _, a := range applied {
		done[a.ID] = true
	}

	var out []Migration
	for _, m := range migrations {
		if !done[m.ID] {
			out = append(out, m)
		}
	}
	return out, nil
}

func nextBatch(ctx context.Context, db *DB) (int, error) {
	applied, err := AppliedMigrations(ctx, db)
	if err != nil {
		return 0, err
	}
	batch := 0
	for _, a := range applied {
		if a.Batch > batch {
			batch = a.Batch
		}
	}
	return batch + 1, nil
}

func ensureMigrationsTable(ctx context.Context, db *DB) error {
	// TEXT, INTEGER and TIMESTAMP are the three types every supported database
	// spells the same way. There is no DEFAULT: the values come from Go, so the
	// tracking table needs no database function.
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS `+MigrationsTable+` (
		id         TEXT PRIMARY KEY,
		batch      INTEGER NOT NULL,
		applied_at TIMESTAMP NOT NULL
	)`)
	if err != nil {
		return fmt.Errorf("arandu: creating %s: %w", MigrationsTable, err)
	}
	return nil
}

// splitStatements breaks a migration body into individual statements.
//
// It is deliberately naive -- split on ";", drop what is blank -- and that is
// safe for the DDL a migration contains. A migration that needs a semicolon
// inside a literal has outgrown this and belongs in Atlas, in phase 2.
func splitStatements(body string) []string {
	var out []string
	for _, s := range strings.Split(body, ";") {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

func applyOne(ctx context.Context, db *DB, m Migration, batch int) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("arandu: migration %s: begin: %w", m.ID, err)
	}
	defer func() { _ = tx.Rollback() }()

	// The migration body may hold more than one statement, and SQLite's driver
	// refuses a multi-statement Exec.
	for _, statement := range splitStatements(m.Up) {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("arandu: migration %s failed: %w", m.ID, err)
		}
	}

	insert := db.dialect.Rebind(`INSERT INTO ` + MigrationsTable + ` (id, batch, applied_at) VALUES (?, ?, ?)`)
	if _, err := tx.ExecContext(ctx, insert, m.ID, batch, time.Now().UTC()); err != nil {
		return fmt.Errorf("arandu: recording migration %s: %w", m.ID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("arandu: migration %s: commit: %w", m.ID, err)
	}
	return nil
}

func revertOne(ctx context.Context, db *DB, m Migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("arandu: rollback of %s: begin: %w", m.ID, err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, statement := range splitStatements(m.Down) {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("arandu: rollback of %s failed: %w", m.ID, err)
		}
	}

	del := db.dialect.Rebind(`DELETE FROM ` + MigrationsTable + ` WHERE id = ?`)
	if _, err := tx.ExecContext(ctx, del, m.ID); err != nil {
		return fmt.Errorf("arandu: clearing the record of %s: %w", m.ID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("arandu: rollback of %s: commit: %w", m.ID, err)
	}
	return nil
}
