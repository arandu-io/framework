---
name: framework-module
description: The composition contract of the Arandu framework core — foundation.Module, the nine optional interfaces, and the boot sequence that asks for each one. Use when adding or changing a module, when a change touches New, Boot, Run, Shutdown, the middleware pipeline or the router wiring, when reading or extending bootstrap.LoadConfiguration, and when the request mentions "register a module", "boot order", "add a background loop", "add a migration", "add a scheduled task", "health check", "graceful shutdown", "middleware pipeline", "add a config setting", "read an environment variable", "where does this get wired" or "why is my method never called". Covers what the Application does in order, where each optional interface is asked, what a module must never do, and the typed configuration read once at start.
license: MIT
---

# The module contract and the boot sequence

`foundation` is one of the four packages in this module that hold code rather
than forward to `github.com/arandu-io/hesape`, and it is the one that will still
be here after v1.0.0 removes the bridges. What it owns is the `Application`:
the object that composes the process exactly once, at start, never per request.

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
(`foundation/module.go:19-49`): the hesape one names a `*routing.Router` and
this one names a `*http.Router`, which is the one envelope the request bridge
could not turn into an alias, because it carries the renderer and the flash and
`hesape/routing.Router` deliberately carries neither. It becomes an alias the
day `http.Router` stops being an envelope, and not before. The same reasoning
keeps `RendererProvider` and `Locker` declared here; everything else in that
file is an alias.

## The nine optional interfaces, and where each is asked

Every one is asked for by a type assertion, at exactly one place, all of them in
`foundation/application.go`. Get the signature wrong and the assertion simply
fails: the module is registered, nothing complains, and the method is never
called.

| interface | asked at | when |
| --- | --- | --- |
| `RendererProvider` | `:239` | in `Boot`, before any route is registered. Two providers refuse the boot rather than picking one (`:244`) |
| `Bootable` | `:216` | in `Boot`, per module, in registration order. A failure stops the process |
| `ReloadTagger` | `:297` | in `mountInternalRoutes`, development only, first one wins |
| `Background` | `:264` | from `Run`, never from `Boot` |
| `Health` | `:346` | on each request to `/_arandu/health`; the body names the failing module |
| `Migratable` | `:545` | when `Application.Migrations()` is called |
| `Schedulable` | `:561` | when `Application.Tasks()` is called |
| `Diagnostic` | `:578` | when `Application.Diagnose()` is called, for the error page |
| `Closable` | `:524` | in `Shutdown`, in **reverse** registration order |

Confirm the list has not moved before relying on a line number:

```sh
grep -nE '\.\((Bootable|Background|RendererProvider|Closable|Diagnostic|Schedulable|Migratable|Health|ReloadTagger)\)' foundation/application.go
```

All nine except `RendererProvider` are aliases to `hesape/foundation`, and
`TestTheVocabularyIsTheHesapeVocabulary` (`tests/Unit/foundation/module_test.go:18`)
asserts that at compile time, name by name. A rename in hesape that this package
has not followed fails there rather than in a project that imports the old name.

## The sequence, in order

```
New(cfg)     logger, flash, router; the recorder only in development or under a
             tracing secret; the gauges always            application.go:100
Boot(ctx)    findRenderer -> per module: empty name, duplicate name, Bootable,
             Routes -> mountInternalRoutes                              :194
Run(ctx)     startBackground -> newServer -> ListenAndServe -> signal    :474
Shutdown()   srv.Shutdown -> Closable in reverse order                   :513
```

Three rules fall straight out of it:

**A module never starts a goroutine in `Boot`.** `Boot` wires; it does not run.
A loop belongs to `Background`, whose `Start` is called from `Run` — so only the
process that serves runs it, and a migration runner or a one-shot command does
not. `TestBootDoesNotStartBackgroundLoops` and `TestRunStartsBackgroundLoops`
(`tests/Feature/foundation/application_test.go:418, :435`) hold that split.

**Failing to boot stops the process.** There is no degraded mode, and the same
applies to a background loop that fails to start: an application whose scheduler
silently did not start looks healthy and does no scheduled work.

**The renderer is found before any route is registered**, because a route is
wired with the renderer its handlers will use. That is why it is an optional
interface rather than a wiring call an application makes — a line an application
can leave out is a line an application leaves out.

## The pipeline

`Handler()` (`:370`) composes in this order, outermost first: the root logger,
the development reload, the flash, then the application's own middleware, then
the router. The logger has to be outermost or every `Log(ctx)` in a request
falls back to `slog.Default()` and ignores the configured handler and level.

Everything below the logger is wrapped in `exceptInternal` (`:443`), which takes
the application's middleware off anything under `/_arandu/`. That is not
tidiness: the development reload asks once a second which process is answering,
and running that through an application's own rate limit answered "too many
requests" on a page nobody had hammered. `TestTheFrameworksOwnRoutesDoNotSpendTheApplicationsBudget`
(`tests/Feature/foundation/application_test.go:472`) is that bug, kept.

The known open edge is written on `internalPrefix` (`:405-430`): the namespace
has more than one owner, `exceptInternal` reads the path rather than the
registration, and a module that registers `/_arandu/anything` gets a route with
no Recover, no Observe, no security headers, no rate limit and no CSRF check.
Read that comment before adding anything under the prefix.

## The server limits are named, not defaulted

Six constants at `foundation/application.go:58-65`, each with its reason above
it. A field left off the literal is a limit the process silently does not have,
which is why `newServer` is separate from `Run`: `TestTheServerCarriesEveryLimit`
(`foundation/server_internal_test.go:18`) asserts every one without binding a
port, and `TestWriteTimeoutOutlastsReadTimeout` (`:55`) holds the one ordering
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
   holds 15 of them and is named for behaviours, not for fields.

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
go test -race -count=1 -tags 'integration e2e' ./...
bash tests/test-layout-guard.sh
```

`tests/Feature/foundation/application_test.go` holds 19 tests over this
sequence — boot order, duplicate and empty names, fail-fast, reverse shutdown,
the console gate, the background split. If a change to the `Application` does
not touch that file, check whether it should have.
