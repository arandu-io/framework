// The migrator, answered by github.com/arandu-io/hesape/database.
//
// The tracking table has the same name and the same columns on both sides, and
// the batch numbering is the same code, so a database migrated by the old
// binary is a database the new one reads without a step in between.

package data

import (
	"context"

	"github.com/arandu-io/hesape/database"
)

// MigrationsTable is where applied migration ids are recorded.
const MigrationsTable = database.MigrationsTable

// KeyText is how a text column that takes part in a key is declared.
//
// TEXT is the portable spelling for text, and it is the wrong one for anything
// indexed: MySQL stores TEXT off-page and refuses it in a key without a prefix
// length. VARCHAR(255) is accepted by all three.
//
// The rule: TEXT for free-form content nobody indexes, KeyText for an id, a
// tenant, or anything a UNIQUE or an index names.
const KeyText = database.KeyText

// Migration is a versioned, immutable-once-published schema change.
//
// The id carries its own order -- "2026_07_29_000001_create_users_table" -- for
// the reason that convention exists: a migration that sorts differently on two
// machines applies in a different order on two machines.
type Migration = database.Migration

// AppliedMigration is one row of the tracking table.
type AppliedMigration = database.AppliedMigration

// Migrate applies the pending migrations, in the given order, and returns the
// ids it applied.
//
// Everything applied by one call shares a batch number, which is what makes
// Rollback undo a deploy rather than a single migration. Each migration runs
// inside its own transaction together with the insert into the tracking table,
// so a failure halfway cannot leave the schema ahead of the record.
//
// It is a pipeline step and never a call on the way up: with N replicas, N
// migrations race.
func Migrate(ctx context.Context, db *DB, migrations []Migration) ([]string, error) {
	return database.Migrate(ctx, db, migrations)
}

// Rollback undoes the last batch and returns the ids it reverted, most recent
// first.
//
// A migration with an empty Down is refused rather than skipped: silently
// leaving part of a batch in place is how a rollback produces a schema that
// matches neither version.
func Rollback(ctx context.Context, db *DB, migrations []Migration) ([]string, error) {
	return database.Rollback(ctx, db, migrations)
}

// Status returns every declared migration with the batch it was applied in, or
// zero when it is still pending. It is what `aru migrate:status` prints.
func Status(ctx context.Context, db *DB, migrations []Migration) ([]AppliedMigration, error) {
	return database.Status(ctx, db, migrations)
}

// AppliedMigrations returns the tracking table, ordered by batch and id.
func AppliedMigrations(ctx context.Context, db *DB) ([]AppliedMigration, error) {
	return database.AppliedMigrations(ctx, db)
}

// Pending returns the migrations that have not been applied yet, in order.
func Pending(ctx context.Context, db *DB, migrations []Migration) ([]Migration, error) {
	return database.Pending(ctx, db, migrations)
}
