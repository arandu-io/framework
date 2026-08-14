module github.com/arandu-io/framework

go 1.26

// This module is Laravel's laravel/framework, and hesape is its src/Illuminate.
// The components live in a module of their own because Go publishes a submodule
// without a subtree splitter -- laravel/framework carries src/Illuminate plus a
// 7 MB splitsh-lite binary to do the same job (ADR 0049).
//
// github.com/arandu-io/* is ours and does not spend the dependency budget. What
// the budget forbids is a dependency the core did not write and cannot fix, and
// there is still exactly one of those: golang.org/x/crypto, where argon2 lives.
// It is indirect now because the hashing moved to hesape/hashing -- nothing here
// hashes a password any more. Add a fourth line to this file and the CI fails.
// See docs/adr/0004-core-sem-dependencia.md.
//
// The four adapter repositories this comment used to list -- database, kv, queue
// and storage -- were folded into hesape by ADR 0048. The heavy driver still
// sits in a submodule with its own go.mod, because Go has no optional
// dependency, so a project still pays in go.sum only for what it imports.
require github.com/arandu-io/hesape v0.1.0

require (
	golang.org/x/crypto v0.53.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
)
