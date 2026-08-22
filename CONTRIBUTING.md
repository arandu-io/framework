# Contributing

## Sign your commits

Every commit needs a `Signed-off-by` line:

```
git commit -s -m "what changed and why"
```

That line is the [Developer Certificate of Origin](https://developercertificate.org/):
you are stating that you wrote the change, or that you have the right to submit
it under this project's license. It is not a copyright assignment — you keep
your copyright, and this project can never be relicensed behind your back.

We use DCO rather than a CLA on purpose. A CLA would let the project relicense
later, and the price is that every contributor has to sign a legal document
before their first patch.

## Before you open a pull request

```
gofmt -l $(find . -name '*.go' -not -path '*/testdata/*' -not -name '*.kyse.go')   # no output
go vet ./...
go test -race ./...
```

CI runs these, along with several other checks; `.github/workflows/ci.yml` is
what decides, and reading it beats any list kept here, which goes stale. One
rule is worth knowing before you write the patch, because it is the one that
sends a patch back: the core depends on the standard library and
`golang.org/x/crypto`, and nothing else. A pull request that adds a dependency
there needs to argue for it first, in an issue.

## Where a test goes

Two places, and the choice between them is not a preference. A test that
exercises a package through what it exports goes in `tests/`. A test that needs
an identifier the package does not export goes beside that code, named
`*_internal_test.go` -- Go grants that access only to a file declaring the
package itself, which is a file in the package's own directory, so the
exception is anchored there by the compiler rather than by taste.
`tests/test-layout-guard.sh` runs in CI and fails anything else outside
`tests/`.

The suite tree is split by how much has to be running, and the split is
directories rather than a filename suffix, because `go test` only runs a file
whose name ends in `_test.go` and a naming scheme that competes with that rule
switches the suite off without failing:

| directory | what belongs there |
|---|---|
| `tests/Unit/` | one thing, with nothing running |
| `tests/Feature/` | a whole behaviour, across the layers that produce it |
| `tests/E2E/` | the sequence of requests a client actually makes |

The directory is capitalised and the package clause is not -- `package unit`,
`package feature`, `package e2e` -- and the guard checks that too, along with
the rule that nothing in production may import the tree: it pulls in `testing`,
and a package that reaches it registers a test binary's flags into whatever
imports it.

`go test` attributes coverage per directory, and that is what the layout
trades: the suite tree credits itself, so `-coverpkg` is how you ask for the
number against the packages under test. The internal test is the other side of
the same rule -- it sits where the code it reaches sits, and so it is the one
that credits that package directly. Take it only when you use the access it
takes -- `plans/testpackages.go` in the arandu-io working tree checks exactly
that, by intersecting the identifiers a test names with what its package
declares unexported, and the checklist runs it across every Go repository in the project.

A `package main` has no external form: it cannot be imported, so its tests are
internal and that is the end of it. They still carry the `_internal_test.go`
name, because the guard reads the name.

## What the commit message says

What changed and why. The why is the part that is not in the diff, and it is the
part someone will need in two years.

No AI attribution of any kind: no `Co-Authored-By` for an assistant, no
"generated with" footer. Commits are authored by the people who submit them.

## Architecture decisions

The decisions this project has already made live at arandu.io/docs, and every
one that closed a door has an ADR. If your change contradicts one, say so in the
pull request and argue for the change of decision — that is a normal thing to
do, and it is better than a patch that quietly works around it.
