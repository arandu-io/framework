<p align="center">
  <img src=".github/logo.png" alt="Arandu" width="140" height="140">
</p>

<h1 align="center">arandu-io/framework</h1>

<p align="center">Application bootstrap, typed configuration, and the authorization every repository call goes through.</p>

<p align="center">
<a href="https://github.com/arandu-io/framework/actions/workflows/ci.yml"><img src="https://github.com/arandu-io/framework/actions/workflows/ci.yml/badge.svg" alt="Build Status"></a>
<a href="https://pkg.go.dev/github.com/arandu-io/framework"><img src="https://pkg.go.dev/badge/github.com/arandu-io/framework.svg" alt="Go Reference"></a>
<a href="https://github.com/arandu-io/framework/tags"><img src="https://img.shields.io/github/v/tag/arandu-io/framework?label=version" alt="Latest Version"></a>
<a href="LICENSE.md"><img src="https://img.shields.io/github/license/arandu-io/framework" alt="License"></a>
</p>


## About Arandu

> **Note:** this repository holds the core of the framework. To build an
> application with it, run `aru new <name>`, or start from
> [arandu-io/arandu](https://github.com/arandu-io/arandu).

Arandu is a Go framework for web applications, services and APIs, built around
three things, in this order: **development speed** — a small, predictable
surface, and a generator that emits a full module (screens, migration, policy)
in one shot; **performance** — a single compiled binary, HTML over HTMX instead
of a JavaScript bundle, so there is no template runtime, no hydration step and
no Node in the request path; and **authorization the compiler charges for** — a
`Grant` has only unexported fields, and reaching the database without one does
not compile.

## What it delivers

- **Authorization that cannot be skipped** — `Grant` cannot be built by writing
  a struct literal outside the package that issues it, and every repository
  signature requires one before the id. `TestRepositoryWithoutGrantDoesNotCompile`
  proves it by running the compiler over two fixtures and requiring the exact
  failure: `not enough arguments in call to repo.Find` for a call with no
  Grant, `cannot refer to unexported field valid` for one that tries to forge
  one.
- **Tenant scoped at the source** — the tenant is read from the Grant with
  `Tenant(g)`, never from a path, a body, a query or a header, and
  `ValidTenant` refuses anything outside `^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`.
- **One boot sequence** — [`foundation`](https://pkg.go.dev/github.com/arandu-io/framework/foundation)
  composes the process exactly once, at start, never per request.
- **Background work that survives a crash** — jobs carry a Grant, domain
  events are written to an outbox in the same transaction as the row that
  caused them, and the scheduler holds a lock per replica so N copies of the
  process do not run the same task N times.
- **Diagnosis without an extra install** — a console, a request timeline and
  an N+1 detector live in the core rather than a plugin, and allocate nothing
  when they are off.

Be honest about the limit: `SystemGrant` is the named escape hatch, and it is
exported — a handler *can* construct a Grant nobody authorized. What catches
that is `aru doctor`, a lint, not the type system, with the rules
`system-grant-outside-scope` and `system-grant-without-tenant`.

Zero direct third-party dependencies. `golang.org/x/crypto` arrives indirectly,
through `hesape`, which is the only place it is used; CI refuses a second one.
11,091 lines of production code and 10,798 of test, across 49 test files —
`go test -race ./...` passes.

Today, most of what this module exports — `security`, `data`, `http`, `jobs`,
`events`, `mail`, `observability`, `storage`, `validation`, `arandutest` — is a
compatibility alias: the implementation moved to the sibling `hesape`
repository, and each package's own doc comment names the import path that
replaces it. They are removed in v1.0.0. What stays here for good is process
bootstrap (`foundation`) and typed configuration (`config`).

## Install

```sh
go get github.com/arandu-io/framework
```

## Learning Arandu

The API reference is generated from the doc comments and lives on
[pkg.go.dev](https://pkg.go.dev/github.com/arandu-io/framework). Every exported
symbol carries one, and that is deliberate: it is the documentation that cannot
drift from the code, because it sits in the same file.

The CLI documents itself. `aru help` lists every command, and each one explains
what it writes and what to do with it. `aru doctor` explains what it found and
what breaks, not which rule was violated.

A guide and a website do not exist yet, and that is a decision rather than a
gap: a guide written against an API that still moves is work done twice, and the
second time is worse — there is wrong documentation published. The site is the
next phase, and it will be an Arandu application.

## The rest of Arandu

`aru` is the command line; `arandu` is the project skeleton it clones; `hesape`
is the 47-package collection this module is built from; `examples` is a
complete application to read. `database`, `kv`, `queue` and `storage` are the
storage adapters.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Before opening a pull request, the three
commands at the top of that file have to pass, and CI runs exactly them.

## Security Vulnerabilities

Please review [our security policy](SECURITY.md) on how to report a
vulnerability. Never open a public issue for one.

## License

Open-sourced software licensed under the [MIT license](LICENSE.md).
