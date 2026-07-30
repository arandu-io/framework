// Package auth is the first first-party module and the canonical reference:
// every module the CLI generates has exactly this shape.
//
// Files of a module (vertical layout, not one directory per layer):
//
//	module.go       -> registration and routes
//	user.entity.go  -> the entity
//	user.policy.go  -> who may do what
//	user.repo.go    -> data access, requires a Grant
//	user.service.go -> business rules
//	user.request.go -> input types and Validate
package auth

import (
	"context"

	"github.com/arandu-io/framework/httpx"
	"github.com/arandu-io/framework/kernel"
)

// Module registers the authentication routes.
type Module struct {
	svc *Service
}

// New returns the module. The service is built by the caller, because the wiring
// is explicit: if you want to know where UserRepo comes from, it is written in
// the application's main.
func New(svc *Service) *Module { return &Module{svc: svc} }

// Compile-time proof that the module honors the kernel contracts it claims.
var (
	_ kernel.Module     = (*Module)(nil)
	_ kernel.Migratable = (*Module)(nil)
	_ kernel.Health     = (*Module)(nil)
)

// Name is the module identifier.
func (m *Module) Name() string { return "auth" }

// Routes registers the module's routes.
func (m *Module) Routes(r *httpx.Router) {
	g := r.Group("/auth")
	g.Get("/login", m.showLogin)
	g.Post("/login", m.doLogin)
	g.Post("/logout", m.doLogout)
}

// Health reports whether the module can reach its storage. It feeds
// /_arandu/health, so a database that went away turns into a failing probe
// rather than a stream of 500s.
func (m *Module) Health(ctx context.Context) error {
	return m.svc.repo.db.PingContext(ctx)
}

// Migrations declares the schema this module owns.
//
// Roles are jsonb rather than text[]: a Postgres array needs a driver specific
// type to scan, and the core has no driver dependency. gen_random_uuid is
// built into PostgreSQL 13 and later.
func (m *Module) Migrations() []kernel.Migration {
	return []kernel.Migration{{
		ID: "20260729_0001_create_users",
		Up: `CREATE TABLE users (
			id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
			tenant_id   uuid NOT NULL,
			email       text NOT NULL,
			password    text NOT NULL,
			roles       jsonb NOT NULL DEFAULT '[]'::jsonb,
			created_at  timestamptz NOT NULL DEFAULT now(),
			UNIQUE (tenant_id, email)
		);
		CREATE INDEX users_tenant_created_idx ON users (tenant_id, created_at, id);`,
		Down: `DROP TABLE users;`,
	}}
}
