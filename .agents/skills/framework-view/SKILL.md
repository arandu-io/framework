---
name: framework-view
description: The view runtime of the Arandu framework core — the functions a compiled .kyse.go calls, the three package-level registries behind them, and the two files that still hold an implementation. Use when adding or changing a runtime function generated views call (Text, TextAttr, TextURL, TextJS, TextCSS, Yield, RenderInto, Include, CSRF), when touching Register, RegisterLayout, RegisterAsset or RegisterStylesheet, when changing view.Page or view.Layout, when a page renders blank or reports "no view named x" over a file that is on disk, when an asset or the reload script is not served, and when the request mentions "escaping", "add a directive", "the renderer", "embedded assets", "app.css", "live reload" or "THIRD_PARTY". Covers what is bridged and what is not, why a second registry is the failure to avoid, and the assets directory that is compiled into nothing.
license: MIT
---

# Changing the view runtime

`view` is the framework side of the view layer: the runtime a compiled view
calls, the registries it writes into, the served assets, and the chrome a screen
hands its layout. The views themselves are not here — `resources/views/` belongs
to the project, because it is edited, and the rendering machinery belongs here,
because it is not.

The error page deliberately does not use this package. It has to render when the
rest is broken, including when the view build failed, so it stays as
`html/template` inline in `observability/errorpage`. Do not route it through
here to reuse a component.

## What is bridged and what is not

Seven files, and the split is the first thing to check:

| file | holds |
| --- | --- |
| `runtime.go` | bridge — 11 functions generated code calls, each one line into `hesape/view` |
| `render.go` | bridge — `Register`, `Registered`, `Renderer`, `NewRenderer`, `WrongData` |
| `assets.go` | bridge — `AssetPath`, `Stylesheet`, `AssetHash`, `RegisterStylesheet`, `RegisterAsset`, `Assets`, `URL`, `Handler`, `Version` |
| `reload.go` | bridge — `ReloadTag` |
| `page.go` | **implementation**: `Layout`, `Page` and its methods, `New` |
| `module.go` | **implementation**: the `foundation.Module` that mounts the asset route and supplies the renderer |
| `doc.go` | which hesape package answers, and what is not bridged |

So a change to what `Text` or `RenderInto` *does* is a change in
`github.com/arandu-io/hesape/view`. What belongs here is the line that reaches
it. Read `.agents/skills/framework-bridge/SKILL.md` first.

`Page` and `Layout` are the exception, and `view/doc.go:33-59` states why they
cannot be aliased: `hesape/view` renamed the four error accessors —
`FieldError` → `First`, `FieldErrors` → `Get`, `HasErrors` → `Any`,
`ErrorSummary` → `All` — and `github.com/arandu-io/kyse` declares a two-method
interface asking for `FieldError`, in a separate module. An alias compiles here
and breaks the component library in silence. An envelope cannot stand in either:
`Page` is written as a composite literal across the skeleton and the published
screens, and promoted fields are not addressable in one.

## The three registries, and the one failure to avoid

`Register`, `RegisterLayout` and `RegisterAsset` write into **package-level
tables**, and those tables are hesape's. That is the reason those files bridge
rather than hold a copy: a framework that kept its own would have a view
registered through one name and looked up in the other, which renders as "no
view named x" over a file that is right there on disk.

Three tests hold each table, and they are the shape any new registry has to
satisfy:

```
TestTheViewRegistryIsOneTable    tests/Unit/view/bridge_test.go:61
TestTheLayoutRegistryIsOneTable  tests/Unit/view/bridge_test.go:91
TestTheAssetTableIsOneTable      tests/Unit/view/bridge_test.go:120
```

The asset one has a caller outside this module: `kyse` vendors font faces
through `RegisterAsset` and then reads `Assets()` to write the URL into a
stylesheet. Two tables would put the face in one and serve out of the other,
which is a 404 on an address the stylesheet had just written.

## Adding a function generated code calls

The rules the existing eleven follow:

**The escaping is the caller's, and the name says which.** `Text` and
`UnsafeText` convert identically — both call `hview.Text` — and exist as two
names because under one name the escaped and the raw form are indistinguishable
in the generated file and in a search over a project. Every call to
`UnsafeText` is a place where the value has to be markup that is already
trusted.

**A value that cannot be made safe is refused rather than cleaned.** `TextURL`
checks the scheme against a list of what is allowed, exactly, so a value hiding
a scheme behind whitespace fails to match instead of being tidied — cleaning
would change what the page says without saying so. `TextCSS` does the same and
names the character it refused. Both return the empty string alongside the
error, so a caller that ignores the error writes nothing rather than the value.

**Nothing reaches for request state.** `CSRF` takes the token off the data,
through an interface the page data satisfies. A template that reached a global
for it is how a form ends up carrying another session's token under load.

**A missing thing that is not an error stays not an error.** `Yield` answers the
empty string for a section a given child does not declare: a missing section is
a page without a sidebar.

Add the function in `hesape/view`, add the one-line bridge here with its own doc
comment, and add the round trip to `tests/Unit/view/bridge_test.go` —
`TestTheOldNamesStillEscapeAndInterpolate` (`:236`) is where the interpolation
functions are proven to reach through.

## The assets directory is compiled into nothing

There is no `go:embed` directive left in this module:

```sh
grep -rn "go:embed" --include='*.go' .    # three hits, all inside comments
```

The bytes a browser receives are hesape's, byte-identical to the six files under
`view/assets/`. That copy is left in place deliberately: `THIRD_PARTY.md` at the
root of this module is the copyright notice for exactly those file names, and
`tests/Unit/view/third_party_test.go` is what keeps the notice from rotting —
`TestEveryEmbeddedAssetIsCredited` (`:36`), `TestTheCreditedVersionsAreTheEmbeddedOnes`
(`:61`), `TestTheLicenseTextsAreComplete` (`:93`). Neither the notice nor the
test is inside the package, so neither can be fixed from there.

If you add or replace a file under `view/assets/`, update `THIRD_PARTY.md` in
the same change. The repositories are public and the assets ship inside every
user's binary, which makes this the one item in the project with actual legal
exposure.

`TestNoNodeAnywhere` (`tests/Unit/view/no_node_test.go:22`) is the other
standing rule: no `package.json`, no lockfile, no `node_modules`, no
`vite.config`. Having a build step is allowed; being Node is not.

## Content-Security-Policy decides more than it looks like

Pages are served under `script-src 'self'`. That is why the development reload
is registered as an asset and referenced by its content-addressed URL rather
than written as an inline `<script>` — an inline tag is refused by the policy
**silently**, which reads as the feature simply not working. Any proposal that
needs a string compiled into a function at run time needs a different policy,
and that is a conversation rather than a patch.

## `Page` carries things that must not leak

`Page` implements `MarshalJSON` and `LogValue` to keep the CSRF token out of
JSON and out of logs, and a screen that embeds `Page` inherits both. Seven tests
hold that (`tests/Unit/view/page_redaction_test.go`), including
`TestAnEmbeddingScreenInheritsTheRedaction` (`:154`). If you add a field to
`Page`, decide which of the two it belongs in before you add it, and add the
test in that file.

`TestPageStillSatisfiesLayout` (`tests/Unit/view/page_flash_test.go:138`) is the
assertion that `Page` is still the implementation the delivered layout asks for.
Changing the `Layout` interface without changing `Page` fails there rather than
in a project whose pages stopped rendering.

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

The `.kyse.go` filter on `gofmt` matters even though this module holds no view
source: `gofmt` is the one tool in the chain that ignores a build tag, and a
`.kyse.go` opens with directives that are not valid Go.
