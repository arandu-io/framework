module github.com/arandu-io/framework

go 1.25

// The arandu core takes no third-party dependency beyond golang.org/x/crypto,
// which is where argon2 lives: password hashing is not in the standard library
// and is not something to write by hand.
//
// The adapters are repositories of their own, one driver per module, so a
// project pays in go.sum only for what it imports:
//
//	github.com/arandu-io/database  -- SQLite, Postgres and MySQL (ADR 0014)
//	github.com/arandu-io/kv        -- cache, locks and sessions over RESP
//	github.com/arandu-io/queue     -- queue backends (ADR 0016)
//	github.com/arandu-io/storage   -- object storage (ADR 0016)
//
// This is a product decision rather than a matter of style: see
// docs/adr/0004-core-sem-dependencia.md.
require golang.org/x/crypto v0.31.0

require golang.org/x/sys v0.28.0 // indirect
