// Package data defines the data access contract.
//
// There is no ORM. Queries are plain parameterized SQL, written by hand in the
// templates `aru make:module` emits, which keeps the query plan predictable and
// the value always in a placeholder. What this package adds on top is:
//
//  1. a security.Grant required by every operation (the mandatory path);
//  2. tenant scoping taken from the Grant, never from a parameter;
//  3. automatic instrumentation into the Collector.
//
// This package is a bridge. It is removed in v1.0.0; import github.com/arandu-io/hesape/database directly.
//
// The components moved to github.com/arandu-io/hesape, where the package is
// called database, and this package is now the old name pointing at them. It
// answers to two hesape packages:
//
//	hesape/database  Repository, Query, DB, Dialect, Transaction, Migration
//	hesape/auth      Tenant
//
// The death date above is what keeps this from being a second way to import one
// type. Nothing here holds an implementation: where the name and the signature
// survived the move it is a Go alias, and where the design diverged it is an
// envelope that translates and nothing more.
//
// # Tenant is the one symbol that answers to another package
//
// Tenant is one line -- g.Subject().Tenant -- and living in the package that
// owns the SQL forced the cache and the filesystem to import the database in
// order to read a tenant. It is auth.Tenant instead. The guarantee does not
// move with it: the tenant still comes from the Grant, never from a path, a
// body, a query or a header.
//
// # The one envelope, and what diverged
//
// Repository is declared here rather than aliased, because hesape/database
// changed the return of List from ([]T, error) to (Page[T], error). See the
// type for the whole argument.
package data
