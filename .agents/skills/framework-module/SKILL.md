---
name: framework-module
description: The composition contract of the Arandu framework core — foundation.Module, the optional interfaces, and the boot sequence that asks for each one. Use when adding or changing a module, when a change touches New, Boot, Run, Shutdown, the middleware pipeline or the router wiring, when reading or extending bootstrap.LoadConfiguration, and when the request mentions "register a module", "boot order", "add a background loop", "add a migration", "add a scheduled task", "publish files into a project", "health check", "graceful shutdown", "middleware pipeline", "add a config setting", "read an environment variable", "where does this get wired" or "why is my method never called". Covers what the Application does in order, where each optional interface is asked, what a module must never do, and the typed configuration read once at start.
license: MIT
---

# The module contract and the boot sequence

`foundation` is one of the packages that hold permanent Framework code, rather
than forward to `github.com/arandu-io/hesape`, and it is the one that
will still be here after v1.0.0 removes the bridges. What it owns is the
`Application`: the object that composes the process exactly once, at start,
never per request.

## Classify ownership before composition

The module contract does not decide where a feature belongs. Route it first:

- reusable native capability goes in `hesape/<component>`
- application-owned Model, Policy and Service go in the starter application's
  `app/` tree
- application-owned migrations go in the starter application's
  `database/migrations` tree
- `framework/modules` is reserved exclusively for external community packages
  originating from `package-skeleton`

`modules/auth` is migration debt, never a precedent or permanent Framework
ownership. A request to add native surface there must be rerouted before this
composition procedure is applied.

## The contract

```go
type Module interface {
	Name() string
	Routes(r *http.Router)
}
```

Two methods, and that is the whole required surface. A module with no HTTP
surface still implements `Routes` and registers nothing — a one-line no-op says
the absence better than a second interface to check for.

`Module` is **declared here rather than aliased** to
`hesape/foundation.Module`, and the reason is on the type
(`foundation/module.go`): the hesape one names a `*routing.Router` and
this one names a `*http.Router`, which is the one envelope the request bridge
could not turn into an alias, because it carries the renderer and the flash and
`hesape/routing.Router` deliberately carries neither. So the contract is `Name`
plus `hesape/foundation.Routable`: a hesape module that implements `Routable` is
a `Module` as it stands, and one still on the handler-only router is registered
through `Component`. It becomes an alias the day `http.Router` stops being an
envelope, and not before. The same reasoning
keeps `RendererProvider`, `Ready`, `Locker` and `OccurrenceClaimer` declared
here — `hesape/foundation` has no counterpart for any of them — and everything
else in that file is an alias.

## The optional interfaces, and where each is asked

Every one is asked for through `capable[T]` (`foundation/component.go`), which
asks the module a `Component` wrapped and not the wrapper, at exactly one place.
Ten of them are in `foundation/application.go`, `Publishable` is asked one call
further in, and `Commandable` is asked where the console kernel collects the
commands. Get the signature wrong and the assertion simply fails: the module is registered,
nothing complains, and the method is never called.

| interface | asked at | when |
| --- | --- | --- |
| `RendererProvider` | `:560` | in `Boot` (through `findRenderer`), before any route is registered. Two providers refuse the boot rather than picking one (`:565`) |
| `Bootable` | `:319` | in `Boot`, per module, in registration order. A failure stops the process |
| `ReloadTagger` | `:629` | in `mountInternalRoutes`, development only, first one wins |
| `Background` | `:585` | from `Run`, never from `Boot` |
| `Health` | `:777` | on each request to `/_arandu/health`; the body names the failing module |
| `Ready` | `:781` | in the same loop, straight after `Health`. A module that implements both is asked both, so gaining the second never drops the first |
| `Migratable` | `:1245` | when `Application.Migrations()` is called |
| `Schedulable` | `:1261` | when `Application.Tasks()` is called |
| `Publishable` | `hesape/foundation/publishing.go:95` | when `Application.Publications()` hands each module over, one at a time (`:1282`) |
| `Diagnostic` | `:1302` | when `Application.Diagnose()` is called, for the error page |
| `Closable` | `:1224` | in `Shutdown`, in **reverse** registration order |
| `Commandable` | `foundation/console_builtins.go:308` | when `Create` composes the console, or the first `HandleCommand` of an Application made by `New`; a name another declarer already took refuses, naming both |

Confirm the list has not moved before relying on a line number:

```sh
grep -nE 'capable\[' foundation/*.go | grep -v _test
```

It prints the twelve above, and three more the table leaves out because they
are not a module's own surface: `CSRFExempt` and `JobHandlers`, which the
collection step in `foundation/collect.go` asks, and the unnamed
`CloseAdmission` that `Shutdown` asks before it stops the server.

`Publishable` is the one that grep does not find, and the reason is worth
knowing: `Application.Publications()` passes each module to `Publications`
(`foundation/publishing.go:68`), a wrapper over the hesape function, and the
assertion is in there — beside the check that refuses a tag from outside the
closed set, which is enforced there because it is the one place every caller
passes through.

`Publishes()` is the shape of `Migrations()` and `Tasks()` again: the module
declares, the Application collects, and something else runs. What it declares is
a `Publication`, which is data rather than behaviour — a tag, the `fs.FS` the
module embedded, the directory inside it to take, and the directory in the
project the files land in. The tag is one of six — view, component, config,
migration, translation, asset — and the set is closed, so a seventh is refused
when the publication is read, naming the module, rather than becoming a
directory nobody expected. Nothing here writes a file: `aru vendor:publish`
forwards to the project's own binary to do the writing, because the list of
registered modules exists only inside that binary. The worked example is
`ayra/module`: one embedded tree, tagged `PublishView` because the files are
screens the project edits, landing in `cmd/native` because that is where they
have to be. A tag says what a file is, never where it goes.

Ten of those twelve are aliases to `hesape/foundation`; `RendererProvider` and
`Ready` are declared here. Two tests assert the aliases at compile time, name by
name: `TestTheVocabularyIsTheHesapeVocabulary`
(`tests/Unit/foundation/module_test.go:20`) for the composition names, and
`TestThePublishingVocabularyIsTheHesapeVocabulary` (`:52`) for `Publishable`,
`Publication`, `PublishTag` and the six tags. A rename in hesape that this
package has not followed fails there rather than in a project that imports the
old name.

## The sequence, in order

```
New(cfg)     logger, flash, router; the recorder only in development or under a
             tracing secret; the gauges always            application.go:180
Boot(ctx)    findRenderer -> per module: empty name, duplicate name, Bootable,
             Routes -> mountInternalRoutes                              :289
Run(ctx)     startBackground -> prepareServer -> ListenAndServe -> signal :1133
Shutdown()   CloseAdmission -> srv.Shutdown -> Closable in reverse order :1186
```

Three rules fall straight out of it:

**A module never starts a goroutine in `Boot`.** `Boot` wires; it does not run.
A loop belongs to `Background`, whose `Start` is called from `Run` — so only the
process that serves runs it, and a migration runner or a one-shot command does
not. `TestBootDoesNotStartBackgroundLoops` and `TestRunStartsBackgroundLoops`
(`tests/Feature/foundation/application_test.go:734, :769`) hold that split.

**Failing to boot stops the process.** There is no degraded mode, and the same
applies to a background loop that fails to start: an application whose scheduler
silently did not start looks healthy and does no scheduled work.

**The renderer is found before any route is registered**, because a route is
wired with the renderer its handlers will use. That is why it is an optional
interface rather than a wiring call an application makes — a line an application
can leave out is a line an application leaves out.

## The pipeline

`Handler()` (`:826`) composes in this order, outermost first: the root logger,
the application name, the translator, the validation factory, the development
reload (`foundation.DevReload`, which comes from hesape), then a split between
the framework's own routes and the application's. The application's side is the
flash, then the application's own middleware, then the router. The logger has to
be outermost or every `Log(ctx)` in a request falls back to `slog.Default()` and
ignores the configured handler and level.

The split decides by the route that answers, never by how a path begins: a route
the framework mounted under `/_arandu/` skips the flash and the application's
middleware and gets the framework's own security headers, and an application
route reachable under that prefix is the application's traffic. That is not
tidiness: the development reload asks once a second which process is answering,
and running that through an application's own rate limit answered "too many
requests" on a page nobody had hammered. `TestTheFrameworksOwnRoutesDoNotSpendTheApplicationsBudget`
(`tests/Feature/foundation/application_test.go:806`) is that bug, kept.

`Boot` refuses every route an application or third-party module registers under
`/_arandu/`. The view module is the second legitimate owner beside the
Application, and carries a marker from `internal/routes`; Go refuses that import
outside this module. The check also verifies the direct dynamic type's package
path, so embedding `view.Module` in an external type and replacing `Routes`
does not inherit ownership. The two public-seam proofs are in
`tests/Feature/foundation/reserved_routes_test.go`.

## The server limits are named, not defaulted

Six constants at `foundation/application.go:71-78`, each with its reason above
it. A field left off the literal is a limit the process silently does not have,
which is why `newServer` is separate from `Run`: `TestTheServerCarriesEveryLimit`
(`foundation/server_internal_test.go:38`) asserts every one without binding a
port, and `TestWriteTimeoutOutlastsReadTimeout` (`:473`) holds the one ordering
between them that matters.

## The typed configuration

`bootstrap.LoadConfiguration()` reads the environment — and a `.env`, which
fills only what the environment has not already defined — once, and answers one
typed struct per component. There is no key lookup and no registry: a wrong
field does not compile, where a wrong key in a map compiles, returns the zero
value, and shows up on the first request that happened to need it.

To add a setting:

1. Put the field on the **component's own `Config`**, which lives in its own
   hesape package. `Configuration` is the one place that reads the environment
   and fills them in; it is not a home for settings.
2. Read it in the matching `loadX` function in
   `foundation/bootstrap/loadconfiguration.go`, with `config.String`,
   `config.Int`, `config.Bool` or `config.Seconds`.
3. If the default is not the obvious one, say why on the field. Three already
   do, and each is a failure that would otherwise be silent: the session cookie
   is `Secure` when the application URL is https rather than defaulting to
   false, the filesystem is `private` rather than public, and `LOG_LEVEL=debug`
   in production fails the boot instead of being honoured.
4. Add the test. `tests/Unit/foundation/bootstrap/loadconfiguration_test.go`
   holds a test per behaviour rather than per field; add yours beside the ones it resembles.

Two traps in that file are worth reading before touching it:

- **`SESSION_LIFETIME` is in minutes**, and it is the only duration here that is
  not seconds. `config.Seconds` would turn an existing `SESSION_LIFETIME=120`
  into a two-minute session instead of a two-hour one, and everybody would stay
  signed in long enough for it to look like it worked.
- **`LOG_LEVEL` is read once** and used twice, in two shapes: the channels take
  the name and the root logger takes the parsed level. Reading it twice is two
  answers to one variable the day one of them grows a fallback the other does
  not have.

`Configuration.asMap` is built **from** the structs and never the other way
round. A key that appears there without a field behind it configures nothing.

## The gates

```sh
export GOWORK=off
gofmt -l $(find . -name '*.go' -not -path '*/testdata/*' -not -name '*.kyse.go')
go build ./...
go vet ./...
go test -race -count=1 ./...
go test -race -count=1 -tags e2e ./...
bash tests/test-layout-guard.sh
```

`tests/Feature/foundation/application_test.go` holds the tests over this
sequence — boot order, duplicate and empty names, fail-fast, reverse shutdown,
the console gate, the background split. If a change to the `Application` does
not touch that file, check whether it should have.
