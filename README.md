<h1 align="center">arandu-io/framework</h1>

<p align="center">The Arandu framework.</p>

<p align="center">
<a href="https://github.com/arandu-io/framework/actions/workflows/ci.yml"><img src="https://github.com/arandu-io/framework/actions/workflows/ci.yml/badge.svg" alt="Build Status"></a>
<a href="https://pkg.go.dev/github.com/arandu-io/framework"><img src="https://pkg.go.dev/badge/github.com/arandu-io/framework.svg" alt="Go Reference"></a>
<a href="https://github.com/arandu-io/framework/tags"><img src="https://img.shields.io/github/v/tag/arandu-io/framework?label=version" alt="Latest Version"></a>
<a href="LICENSE.md"><img src="https://img.shields.io/github/license/arandu-io/framework" alt="License"></a>
</p>

## About Arandu

> **Note:** this repository holds the core of the framework. To build an
> application with it, start from [arandu-io/arandu](https://github.com/arandu-io/arandu)
> — or run `aru new <name>`, which does it for you.

Arandu is a Go framework for SaaS. Its one claim is in the line above: the
architecture is not a convention the team agrees to follow, it is a shape the
compiler refuses to break.

- [Authorization that cannot be skipped](https://pkg.go.dev/github.com/arandu-io/framework/security) — every repository signature takes a `Grant`, and a `Grant` comes from a Policy. Reaching the database without one does not compile
- [Data access scoped by tenant](https://pkg.go.dev/github.com/arandu-io/framework/data) — the tenant comes from the `Grant`, never from what the caller sent
- [Typed views, compiled](https://pkg.go.dev/github.com/arandu-io/framework/view) — kyse has Blade's directives, and a typo in a field name is a build error at the line you wrote
- [Routing](https://pkg.go.dev/github.com/arandu-io/framework/httpx) — resources, named routes and URL generation over `net/http`
- [Observability as a product](https://pkg.go.dev/github.com/arandu-io/framework/observability) — a console, a timeline and an N+1 detector in the core, with zero allocations when it is off
- [Background work](https://pkg.go.dev/github.com/arandu-io/framework/jobs) — jobs, a [scheduler](https://pkg.go.dev/github.com/arandu-io/framework/scheduler) with a lock per replica, and [events](https://pkg.go.dev/github.com/arandu-io/framework/events) written to an outbox in the same transaction as the row that caused them

One direct dependency: `golang.org/x/crypto`. CI refuses the second.

## Learning Arandu

The API reference is generated from the doc comments and lives on
[pkg.go.dev](https://pkg.go.dev/github.com/arandu-io/framework). Every exported
symbol carries one, and that is deliberate: it is the documentation that cannot
drift from the code, because it sits in the same file.

The CLI documents itself — `aru help` lists every command, and each one explains
what it writes and what to do with it. `aru doctor` explains what it found and
what breaks, not which rule was violated.

A guide and a website do not exist yet, and that is a decision rather than a
gap: a guide written against an API that still moves is work done twice, and the
second time is worse — there is wrong documentation published. The site is the
next phase, and it will be an Arandu application.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Before opening a pull request, the three
commands at the top of that file have to pass, and CI runs exactly them.

## Security Vulnerabilities

Please review [our security policy](SECURITY.md) on how to report a
vulnerability. Never open a public issue for one.

## License

Open-sourced software licensed under the [MIT license](LICENSE.md).
