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

## Unreleased — the components move out, and `httpx` becomes `http`

Three changes, and the first two are import paths rather than behaviour. Nothing
in this section renames an exported identifier: `Grant`, `Context`, `Router`,
`Module`, `Migration` and the rest answer to what they always did.

### `httpx` is `http`

Every import of `github.com/arandu-io/framework/httpx` becomes
`github.com/arandu-io/framework/http`, and `httpx/middleware` moves with it. The
`x` was never a convention — it marked that `net/http` had the word first, and
Illuminate calls the component `Http` (ADR 0047).

A file that imports both aliases **ours**, so `http` goes on meaning `net/http`
as it does in every other Go file:

```go
import (
	"net/http"

	fhttp "github.com/arandu-io/framework/http"
)

func (c *InvoiceController) Index(ctx *fhttp.Context) error
```

A file that does not import `net/http` needs no alias and reads `http.Context`,
which is what a Laravel developer types.

No shim is left behind. `framework/httpx` does not exist, and an import of it
fails to resolve rather than compiling against something stale.

### `kernel` is `foundation`, and `Kernel` is `Application`

`Illuminate\Foundation` is not a published package — of the 37 `illuminate/*`
that `laravel/framework` declares, none is Foundation, because it ships only
inside the framework. Ours now says so: `kernel.Kernel` is
`foundation.Application` (ADR 0049).

`framework/kernel` still works. It is a bridge, and it is removed in v1.0.0 —
every method keeps its name, so the change is the import path and the type name:

```go
app := foundation.New(cfg)   // was kernel.New(cfg), and still is
```

Two symbols did not survive the move, and neither was reachable from an
application: `Locker` moved down into `foundation` under the same name, and
`FormatRoutes` now calls through to `hesape/routing`.

### The application is built from one struct per component

| was | is | what to do |
|---|---|---|
| `kernel.New(config.Config)` | `kernel.New(bootstrap.Configuration)` | Build it with `bootstrap.LoadConfiguration()` and pass the result. `foundation.New` and `(*Application).Config()` follow it |

`config.Config` was one struct of eleven fields read by one function. What
replaces it is `github.com/arandu-io/framework/foundation/bootstrap`, where each
component declares its own settings and `LoadConfiguration` reads the
environment once to fill them in:

```go
cfg, err := bootstrap.LoadConfiguration()
if err != nil {
	return err
}
app := foundation.New(cfg)
```

The fields an application reaches for most:

| was | is |
|---|---|
| `cfg.AppName`, `cfg.Env`, `cfg.HTTPAddr`, `cfg.AppKey` | `cfg.App.Name`, `cfg.App.Env`, `cfg.App.HTTPAddr`, `cfg.App.Key` |
| `cfg.IsDev()` | `cfg.App.Env.Is(config.EnvDev)`, with `github.com/arandu-io/hesape/config` |
| `cfg.SessionTTL` | `cfg.Session.Lifetime` |
| `cfg.Database` | `cfg.Database`, which is `hesape/database.Config` |
| `cfg.LogLevel`, `cfg.TracingSecret`, `cfg.Editor` | `cfg.Observability.LogLevel`, `.TracingSecret`, `.Editor` |

`framework/config` still loads and still validates. It is a bridge from here,
removed in v1.0.0, and nothing in the framework reads it any more.

Two settings have no field yet, and an application that reads `RedisURL` or
`CSRFTTL` off `config.Config` keeps doing so until they do.

One behaviour changed with the move: `LOG_LEVEL` is parsed at boot, so a name
outside the eight the logger knows stops the process instead of restoring a
default. `warn` is spelled `warning`.

Three constants went with it, and none of them was reachable from anything:

| was | is | what to do |
|---|---|---|
| `config.AppKeyLen` | `encryption.KeySize`, in `github.com/arandu-io/hesape/encryption` | It said 32 twice. The key is parsed and validated by `encryption`, which is where the length belongs |
| `config.DefaultSQLitePath` | `database.DefaultSQLitePath`, in `github.com/arandu-io/hesape/database` | Same value, one owner |
| `config.DefaultDatabaseURL` | `database.DefaultURL`, same package | Same |

### The components are their own module

`github.com/arandu-io/hesape` is now a `require` of this one. Nothing in your
code has to import it: every package you already use is still here, as a thin
bridge over the one that answers for it, and every bridge names its replacement
and the release it disappears in.

Where a name changed on the way down, the bridge translates rather than exposing
the new one — `security.SessionStore` still has `Load`, `Rotate`, `Destroy` and
`IDFromRequest`, though `hesape/session` calls them `All`, `Regenerate`,
`Invalidate` and `ID`.

One signature could not be preserved:

| was | is | what to do |
|---|---|---|
| `subject.PasswordConfirmedWithin(d)` | `security.PasswordConfirmedWithin(subject, d)` | It was a method on `Subject`, and `Subject` is now an alias for `auth.Subject` — Go forbids declaring a method on another package's type |

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

## v0.16.0

Nothing broke. Two additions worth knowing about, because they change what is
possible rather than what compiles:

- `security.Guest(tenant)` is an anonymous reader, declared on purpose, and
  `Authorize` lets it reach the policy. A `Subject` nobody filled in is still
  refused before the policy is consulted — the marker is unexported and only
  `Guest` sets it, so a forgotten session load cannot borrow the public path. A
  policy that says nothing about guests denies them, which is what every
  generated policy does. See ADR 0029.
- `(*httpx.Context).URL(name, params...)` builds the path of a named route. The
  table was already there and reachable only from the router, so every
  controller that wanted a link built one by hand.

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
