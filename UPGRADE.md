# Upgrade guide

What changed in a way that stops your code compiling, and what to write instead.

Additions are not listed. A new symbol breaks nothing, and a file that listed
every one of them would be a changelog nobody reads to find the two lines that
matter.

## Before v1.0.0

While the version starts with `v0.`, the API can break. That is what `v0.` means
in Go and it is deliberate — the alternative is freezing a shape before anyone
has built on it. What is not deliberate is breaking it quietly, which is what
this file exists to stop.

Every release from now on is compared against the one before it, package by
package, by `apidiff` in CI. An incompatible change that is not written down
here fails the build.

---

## v0.13.3 — everything published so far

The entries below were measured, not remembered: `apidiff` between `v0.1.0` and
`v0.13.3`, over every package of the module.

They are grouped by package rather than by version. Per-version attribution was
not reconstructed, and saying so is better than guessing at it — the twenty-four
tags predate this file, and from here the CI step produces the attribution as
each release happens.

### `data`

| was | is | what to do |
|---|---|---|
| `Wrap(*sql.DB) *DB` | `Wrap(*sql.DB, Dialect) *DB` | Pass the dialect. `data.ParseDialect(cfg.Database.Connection)` reads it from configuration |
| `AppliedMigrations(ctx, *DB) (map[string]bool, error)` | `AppliedMigrations(ctx, *DB) ([]AppliedMigration, error)` | The map answered "did it run"; the slice also carries the batch, which is what makes `Rollback` undo a deploy rather than one file |
| `Query.Filter` | *removed* | It never filtered anything: no producer set it and no consumer read it. A field that silently does not work is worse than no field, and implementing it would have been a query builder, which RULE 9 refuses. Write the condition in the repository method |

### `httpx`

| was | is | what to do |
|---|---|---|
| `(*Router).Get/Post/Put/Patch/Delete` returned nothing | they return `*Route` | Nothing, unless you assigned the result. The return is what makes `.Name("home")` chain |
| `(*Router).Routes() []Route` | `() []*Route` | A route is now addressable after registration, so it is handed out by pointer |
| `Route.Name` (a field) | `(*Route).Name(string)` sets it, `(*Route).RouteName()` reads it | Reading `r.Name` becomes `r.RouteName()`. The field never held anything: it existed and was never filled |

### `httpx/middleware`

| was | is | what to do |
|---|---|---|
| `Observe(bool, string)` | `Observe(bool, string, *observability.Recorder)` | Pass `k.Recorder()`. Passing `nil` records nothing, which is what production does |

### `observability`

| was | is | what to do |
|---|---|---|
| `Collector.Dumps/Events/External/Queries` (fields) | methods with the same names | Add `()`. They became methods when the Collector started being read from more than one goroutine |
| `HandleDebugConsole` | `NewConsole(...)`, mounted at `ConsolePath` | The console is a module now, registered like any other, rather than a handler you wire by hand |

### `kernel`

| was | is | what to do |
|---|---|---|
| `(*Kernel).Routes() []httpx.Route` | `() []*httpx.Route` | Follows the router change above |
| `FormatRoutes([]httpx.Route)` | `([]*httpx.Route)` | Same |

### `config`

| was | is | what to do |
|---|---|---|
| `Config.DatabaseURL string` | `Config.Database DatabaseConfig` | One URL could not express a pool size, a connection lifetime or a dialect. The `.env` keys are the conventional `DB_*` ones |

### `modules/auth`

| was | is | what to do |
|---|---|---|
| `New(*Service) *Module` | `New(*Service, TenantResolver) *Module` | Pass `auth.FixedTenant(cfg.Auth.Tenant)` for a single-tenant application. A resolver reading the host name is the multi-tenant version, and it is the same call |
| `Module` was comparable | it is not | If you compared two `Module` values with `==`, compare their names |

---

## Deprecation

Nothing is deprecated right now, and that is a statement rather than an
omission: the list above is what has already broken, and there is no symbol
currently on its way out.

When one is, it goes through four steps and this file records each:

1. `// Deprecated:` on the symbol, naming what replaces it
2. a warning from `aru doctor`, not an error
3. an entry here, with the before and after
4. removal, never in the same release as step 1

## How this is checked

`apidiff` runs in CI on every pull request, comparing the working tree against
the latest tag, package by package. Incompatible changes are printed in the
build log, and the build fails when there are some and this file has no entry
for them.

The point is not to prevent the break. It is to make the break a thing somebody
decided, in a diff a reviewer can see, instead of something a person discovers
when their build stops.
