// The migration contract, answered by
// github.com/arandu-io/hesape/database/migrations.
//
// The runner this file used to wrap is gone. A migration is no longer three
// strings a function applies to a *DB: it is a type with an Up method, and the
// Migrator that applies it takes a connection. Reach it directly --
// migrations.NewMigrator with database.MigrationResolver, or
// database.ForMigrations for a single connection.

package data

import (
	"github.com/arandu-io/hesape/database"
	"github.com/arandu-io/hesape/database/migrations"
)

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
// GetName carries the order -- "2026_07_29_000001_create_users_table" -- for the
// reason that convention exists: a migration that sorts differently on two
// machines applies in a different order on two machines. Embed
// migrations.BaseMigration and only GetName and Up are left to write.
type Migration = migrations.Migration
