package data

import (
	"context"
	"fmt"
)

// MigrationsTable is where applied migration ids are recorded.
const MigrationsTable = "arandu_migrations"

// Migration is a versioned, immutable-once-published schema change.
//
// The id carries its own order -- "20260729_0001_create_users" -- because a
// migration that sorts differently on two machines applies in a different order
// on two machines.
type Migration struct {
	ID   string
	Up   string
	Down string
}

// Migrate applies the pending migrations, in the given order, and returns the
// ids it applied.
//
// Each migration runs inside its own transaction together with the insert into
// the tracking table, so a failure halfway cannot leave the schema ahead of the
// record. It stops at the first failure: applying later migrations over a broken
// schema turns one clear error into an unrecoverable database.
//
// The statements target PostgreSQL, the supported database of phase 1. Phase 2
// moves this to Atlas; see docs/03-roadmap-fases.md.
func Migrate(ctx context.Context, db *DB, migrations []Migration) ([]string, error) {
	if err := ensureMigrationsTable(ctx, db); err != nil {
		return nil, err
	}
	done, err := AppliedMigrations(ctx, db)
	if err != nil {
		return nil, err
	}

	var applied []string
	for _, m := range migrations {
		if m.ID == "" {
			return applied, fmt.Errorf("arandu: migration with empty id")
		}
		if done[m.ID] {
			continue
		}
		if err := applyOne(ctx, db, m); err != nil {
			return applied, err
		}
		applied = append(applied, m.ID)
	}
	return applied, nil
}

// AppliedMigrations returns the set of ids already recorded.
func AppliedMigrations(ctx context.Context, db *DB) (map[string]bool, error) {
	if err := ensureMigrationsTable(ctx, db); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT id FROM `+MigrationsTable)
	if err != nil {
		return nil, fmt.Errorf("arandu: reading %s: %w", MigrationsTable, err)
	}
	defer rows.Close()

	done := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		done[id] = true
	}
	return done, rows.Err()
}

// Pending returns the migrations that have not been applied yet, in order.
func Pending(ctx context.Context, db *DB, migrations []Migration) ([]Migration, error) {
	done, err := AppliedMigrations(ctx, db)
	if err != nil {
		return nil, err
	}
	var out []Migration
	for _, m := range migrations {
		if !done[m.ID] {
			out = append(out, m)
		}
	}
	return out, nil
}

func ensureMigrationsTable(ctx context.Context, db *DB) error {
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS `+MigrationsTable+` (
		id         text PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`)
	if err != nil {
		return fmt.Errorf("arandu: creating %s: %w", MigrationsTable, err)
	}
	return nil
}

func applyOne(ctx context.Context, db *DB, m Migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("arandu: migration %s: begin: %w", m.ID, err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, m.Up); err != nil {
		return fmt.Errorf("arandu: migration %s failed: %w", m.ID, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO `+MigrationsTable+` (id) VALUES ($1)`, m.ID); err != nil {
		return fmt.Errorf("arandu: recording migration %s: %w", m.ID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("arandu: migration %s: commit: %w", m.ID, err)
	}
	return nil
}
