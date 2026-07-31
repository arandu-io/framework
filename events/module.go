package events

import (
	"context"

	"github.com/arandu-io/framework/httpx"
	"github.com/arandu-io/framework/kernel"
)

// Module brings the outbox table.
//
// It registers no routes: it exists so the table travels with the framework
// rather than being copied into every project's migrations. Register it in
// cmd/app/main.go next to the modules that store events.
type Module struct{}

// NewModule returns the module.
func NewModule() *Module { return &Module{} }

var (
	_ kernel.Module     = (*Module)(nil)
	_ kernel.Migratable = (*Module)(nil)
)

// Name is the module identifier.
func (*Module) Name() string { return "events" }

// Routes registers nothing. The relay and the event console are phase 3.
func (*Module) Routes(*httpx.Router) {}

// Migrations returns the outbox table.
func (*Module) Migrations() []kernel.Migration {
	return []kernel.Migration{
		{
			ID: "2026_07_31_000001_create_outbox_table",
			// Portable types only: TEXT, INTEGER and TIMESTAMP mean the same
			// thing on SQLite, Postgres and MySQL. jsonb would be one engine's
			// spelling, and the payload is written and read as JSON text either
			// way.
			Up: `
CREATE TABLE outbox (
    id            TEXT PRIMARY KEY,
    tenant_id     TEXT NOT NULL,
    event         TEXT NOT NULL,
    aggregate     TEXT NOT NULL,
    aggregate_id  TEXT NOT NULL,
    payload       TEXT NOT NULL,
    authorized_by TEXT NOT NULL,
    action        TEXT NOT NULL,
    occurred_at   TIMESTAMP NOT NULL,
    published_at  TIMESTAMP,
    attempts      INTEGER NOT NULL DEFAULT 0,
    last_error    TEXT
);

-- The relay reads unpublished events oldest first. A partial index would be
-- tighter, and MySQL does not have one; the two leading columns give the same
-- scan on every engine.
CREATE INDEX idx_outbox_pending ON outbox (published_at, occurred_at);

-- Deduplication is the consumer's job, and the id is the key it deduplicates
-- on. Delivery is at-least-once: the same event can arrive twice, and that is
-- the price of never losing one.
CREATE INDEX idx_outbox_tenant ON outbox (tenant_id, occurred_at);
`,
			Down: `DROP TABLE outbox;`,
		},
	}
}

// Health reports nothing yet. The check that matters -- how long the oldest
// unpublished event has been waiting -- belongs to the relay, in phase 3.
func (*Module) Boot(context.Context) error { return nil }
