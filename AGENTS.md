# Working in this repository

`arandu-io/framework` is the core of the Arandu framework: the boot sequence,
the typed configuration read once at start, the routing envelope, and the
authorization every repository call goes through. There is no application here
and no `main`, and there is one Go module — `find . -name go.mod | wc -l` prints
1 — so `./...` never stops at a module boundary.

Read `.agents/skills/` before writing code. Each file is a procedure, named by
the situation you are in.

## The first thing to know

Twenty packages, and fifteen of them are bridges.

```sh
export GOWORK=off
go list ./... | grep -vc '/tests'                                 # 20
grep -rl "This package is a bridge" --include='doc.go' . | wc -l  # 15
```

Those fifteen are old names pointing at `github.com/arandu-io/hesape`, and every
one of their doc comments carries the same line and the same death date: removed
in v1.0.0. A symbol in one of them is either a Go alias — in which case the two
names are one type to the compiler — or an envelope that translates and holds
nothing. Two are not quite that and say so in their own `doc.go`: `kernel`
points one directory across at `foundation` rather than at hesape, and `config`
is the one bridge that still holds an implementation, because what replaced it
is not a rename.

Five packages hold code, and they are what stays here for good:

| package | what it owns |
| --- | --- |
| `foundation` | the `Application`: `New`, `Boot`, `Run`, `Shutdown`, the pipeline, the module contract |
| `foundation/bootstrap` | `Configuration` and `LoadConfiguration` — the environment read once, typed |
| `http/middleware` | the route guards, CSRF, the flash, rate limit, recover, security headers, the observability middleware |
| `internal/routes` | the sealed capability that identifies first-party owners of the reserved HTTP namespace; Go refuses imports from application and third-party modules |
| `modules/auth` | the first first-party module, and the canonical shape every generated module copies |

**So the first question about any change is which repository it belongs to.**
A behaviour fix inside `security`, `data`, `view`, `jobs`, `events`, `mail`,
`observability`, `scheduler`, `storage`, `validation`, `arandutest`, `http` or
`kernel` almost always belongs in `hesape`, and what belongs here is the line
that reaches it. `.agents/skills/framework-bridge/SKILL.md` is the procedure for
telling those two apart.

## The gates

Nothing is finished until all four exit zero.

```sh
export GOWORK=off
gofmt -l $(find . -name '*.go' -not -path '*/testdata/*' -not -name '*.kyse.go')
go build ./...
go vet ./...
go test -race -count=1 ./...
```

Both filters on `gofmt` are load-bearing. `gofmt` is the one tool in the chain
that ignores a build tag, and `testdata/` here holds two programs that are
invalid on purpose — `data/testdata/missing_grant` and
`data/testdata/forged_grant` are compiled by a test that requires both to be
**refused**.

Two things that command does not reach, and CI runs both:

```sh
go test -race -count=1 -tags 'integration e2e' ./...
bash tests/test-layout-guard.sh
```

`tests/E2E/view/flash_test.go` opens with `//go:build e2e`, and a directory
whose files are all excluded by a build constraint does not fail — it
disappears from `./...`. Without the tag the suite is not skipped, it is not
reported at all: the untagged run prints 29 `ok` lines and the tagged run prints
30. The layout guard is the other one, and its first rule is the
expensive one: a file named `DiskTest.go` compiles as ordinary code and none of
its tests run, with no error and no warning.

## What does not exist here

Reaching for one of these is the fastest way to write something that will be
sent back. None is missing by accident.

| A model reaches for | What is here instead |
| --- | --- |
| a service container, dependency injection | `Application.Register(mods ...Module)`, a slice, booted in the order it was written |
| service providers, auto-discovery | `foundation.Module` — `Name()` and `Routes()` — plus nine optional interfaces the `Application` asks for by type assertion |
| `config("app.name")`, a settings registry | `bootstrap.Configuration`: one typed struct per component, filled once. A wrong field does not compile |
| a component that loads its own settings at first use | one reader, one moment, one error. See the argument on `Configuration` |
| middleware that authorizes | a Policy that issues a `security.Grant`. Middleware answers "is there a session" and stops |
| an ORM, a query builder on the model | `data.Repository`, parameterised SQL, a Grant before the id |
| a template runtime that looks a name up at request time | compiled views registered from `init()`; the registry is package-level and there is exactly one of it |
| `node_modules`, a bundler, a CDN | nothing. There is not even a `go:embed` directive left in this module — the served bytes are hesape's |
| a migration run at process start | `Application.Migrations()` collects; something else runs them |

## The two rules everything else follows from

**Authorization is a value.** `security.Grant` is an alias for
`hesape/auth.Grant`, which has only unexported fields. Every repository method
takes one before the id, so a handler that reaches the database without asking a
Policy has nothing to pass. That claim is proven by compiling rather than
asserted: `TestRepositoryWithoutGrantDoesNotCompile`
(`tests/Feature/data/grant_required_test.go:25`) runs `go vet` over the two
fixtures and requires each to fail with a specific message, because a fixture
that failed for an unrelated reason would prove nothing.

**The tenant comes from the Grant.** `data.Tenant(g)`, never from a path
segment, a body, a query or a header. The one place a tenant is decided
elsewhere is login, where there is no session yet, and `modules/auth` states
that asymmetry on `TenantResolver`.

## Where a change goes

Beside the code it belongs to, in the package whose `doc.go` claims it. Read
that `doc.go` first — in a bridge package it names which hesape package answers
and lists exactly what diverged.

A test goes in `tests/`, in the category that matches how much has to be
running: `tests/Unit/` for one thing with nothing running, `tests/Feature/` for
a whole behaviour, `tests/E2E/` for the sequence of requests a client makes. The
directory is capitalised and the package clause is not — `package unit`,
`package feature`, `package e2e`. The only test allowed outside that tree is one
that genuinely needs an unexported identifier, and it is named
`*_internal_test.go` and sits beside the code. The guard checks all of that.

An exported symbol without a doc comment is not finished. `pkg.go.dev` builds
the reference out of them and that reference is the only documentation this
project publishes. The comment documents the symbol and nothing else: what it
does, what it takes, what it returns, what it guarantees, and the reason a
signature is the shape it is — said in terms of the code. No date, no record
kept elsewhere, no sibling repository's name.

Comments, identifiers, error messages, log lines, CLI output and test names are
in English.
