---
name: framework-bridge
description: Fifteen of this module's twenty packages hold no implementation — they are old names pointing at github.com/arandu-io/hesape and they are removed in v1.0.0. Use before changing anything under security, data, http, view, jobs, events, mail, observability, scheduler, storage, validation, arandutest, config or kernel; when a fix looks like it belongs to one of them; when the request is to "fix this function", "add a method" or "change this behaviour" and the file turns out to be one line long; when adding, renaming or removing an exported symbol anywhere in this module; and when the request mentions "alias", "wrapper", "deprecated", "compatibility", "UPGRADE.md" or "apidiff". Covers how to tell an alias from an envelope, where the change actually goes, the test each bridge carries, and the CI gate that fails a quiet break.
license: MIT
---

# The bridge packages, and where a change to one really goes

Most of this module is names. The implementation moved to
`github.com/arandu-io/hesape`, and what stayed is the old import paths pointing
at it, with a death date on every one.

```sh
export GOWORK=off
go list ./... | grep -vc '/tests'                                 # 20
grep -rl "This package is a bridge" --include='doc.go' . | wc -l  # 15
```

The four permanent implementation packages are `foundation`,
`foundation/bootstrap`, `http/middleware` and `internal/routes`. Everything
else on the bridge list — `security`, `data`, `http`, `view`, `jobs`, `events`,
`mail`, `observability`, `observability/errorpage`, `scheduler`, `storage`,
`validation`, `arandutest`, `config`, `kernel` — is a bridge.

`modules/auth` is the remaining implementation package in the current tree,
but it is migration debt, never a precedent or permanent Framework ownership.
Reusable native capability belongs in `hesape/<component>`; application-owned
Model, Policy and Service belong in the starter application's `app/` tree;
application-owned migrations belong in its `database/migrations` tree; and
`framework/modules` is reserved exclusively for external community packages
originating from `package-skeleton`. Do not add native surface to
`modules/auth`.

## The procedure

**1. Read the package's `doc.go` before the file you came for.** Every bridge
doc names which hesape packages answer it and lists exactly what diverged. The
split is not one-to-one and guessing it is how a change lands in the wrong
place:

```
security/doc.go   answers to five: hesape/auth, session, hashing, encryption, http
http/doc.go       answers to three: hesape/http, routing, pipeline
data/doc.go       answers to three: hesape/database, database/migrations, auth
view/doc.go       answers to one: hesape/view
kernel/doc.go     answers to framework/foundation -- the one bridge that does
                  not point at hesape at all
```

**2. Decide which of the three shapes you are looking at.** The doc comment on
the symbol says which, and it says why.

- **An alias** — `type Grant = auth.Grant`. The two names are one type to the
  compiler. There is nothing here to change: the behaviour is hesape's, and so
  is the fix.
- **A wrapper** — a one-line function calling through, because Go has no alias
  form for a function or for a generic function. `security.Authorize`,
  `data.Tenant`, `foundation.FormatRoutes` and `kernel.New` are these. Still
  nothing here to change.
- **An envelope** — a type declared here that translates, because the hesape
  design diverged in a way an alias cannot absorb. Each states its own argument
  on the type, and each package's `doc.go` enumerates the ones it holds:
  `security` names three (`SessionStore`, `SessionBackend`, `SignInThrottle`),
  `http` names three (`Router`, `Routes`, `Resource`), `data` names one
  (`Repository`), and `events` and `mail` name their own. To find them all:

  ```sh
  grep -n "envelope" */doc.go */*/doc.go
  ```

**3. If it is an alias or a wrapper, the change is in `hesape`.** Say so and
stop. Reimplementing the behaviour here re-creates the second copy the bridge
exists to remove, and it will pass every gate.

**4. If it is an envelope, the change may be here — and it may not change the
signature.** That is the rule the envelopes were written to keep. `data.Repository`
is the worked example: `hesape/database.Repository` changed `List` from
`([]T, error)` to `(Page[T], error)`, the better shape is hesape's, and the
alias is still refused, because every generated repository carries a
`var _ data.Repository[T, string] = (*R)(nil)` line and an alias would break all
of them at once with an error about a return type nobody wrote. A bridge that
changes a signature is not a bridge.

**5. Write the test in the bridge's own file, and only about the bridge.**

```sh
find tests -name 'bridge_test.go' | wc -l   # 14
```

Fourteen of the fifteen carry one. `config` is the exception and it is not an
oversight: it is the one bridge that still holds an implementation rather than
aliases — its `doc.go` says so — so it has ordinary tests under
`tests/Unit/config/` instead.

What a bridge test proves is that the old name reaches the new behaviour, and
nothing else. The pattern is fixed:

- **one compile-time assertion per alias, in both directions.** One direction
  alone would pass for a type that merely converts. `tests/Unit/view/bridge_test.go:33-41`
  is the shape:

  ```go
  var (
      _ view.Func  = hview.Func(nil)
      _ hview.Func = view.Func(nil)
  )
  ```

- **one round trip per envelope**, because an envelope is the place a rename can
  be wired to the wrong method and still compile. `TestSessionStoreReachesTheRenamedMethods`
  (`tests/Unit/security/bridge_test.go:273`) and
  `TestMemoryBackendKeepsTheOldMethodNames` (`:220`) are those.

Do not copy a behaviour test down from hesape. The behaviour is tested there,
against the code that runs, and a second copy is a second place for it to be
described.

## The registries are the reason some files bridge rather than stay

Three tables in `view` are package-level state, and a framework that kept its
own copies would have a view registered in one and looked up in the other —
which reads as "no view named x" over a file that is on disk. That is what
`TestTheViewRegistryIsOneTable`, `TestTheLayoutRegistryIsOneTable` and
`TestTheAssetTableIsOneTable` (`tests/Unit/view/bridge_test.go:61, :91, :120`)
hold. The same argument decides any future bridge over package-level state.

## Adding or removing an exported symbol

CI compares the whole module against the last release with `apidiff` and fails
an incompatible change that did not touch `UPGRADE.md`. Read the step in
`.github/workflows/ci.yml` named `api diff against the last release` before you
rename anything — the comment there records why it is per-module rather than
per-package, and why `fetch-depth: 0` is on the checkout.

Breaking is allowed while the version starts with `v0.`. Breaking **quietly** is
not. If your change removes or renames an exported symbol, write the entry: what
somebody has to change, and why.

## The gates

```sh
export GOWORK=off
gofmt -l $(find . -name '*.go' -not -path '*/testdata/*' -not -name '*.kyse.go')
go build ./...
go vet ./...
go test -race -count=1 ./...
```

And the two that command does not reach — the E2E suite sits behind a build tag,
and a directory whose files are all excluded disappears from `./...` rather than
failing:

```sh
go test -race -count=1 -tags 'integration e2e' ./...
bash tests/test-layout-guard.sh
```
