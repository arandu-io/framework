# Arandu

A Go framework for SaaS. Authorization you cannot bypass, and debugging that
tells you the likely cause.

> It is not the developer who guarantees the architecture. It is the compiler.

**Arandu** is a Tupi-Guarani word for wisdom, intelligence, deep knowledge.
Among the Kaiowá and Guarani the literal reading recorded is *"to hear time"* —
to know through lived experience, in relation to one's surroundings. The name
enters as a dictionary word, not as the name of a mythological figure.

The CLI is called **`aru`**, not `arandu`: the `arandu` binary is already taken
by another project. Same split as Laravel and `artisan`.

## What is different

1. **Authorization is not optional.** `security.Grant` can only be issued by
   `security.Authorize`, and every repository requires one in its signature.
   Reaching the database without passing through a policy is not discouraged —
   it does not compile.

2. **Debugging is core, not a plugin.** One `Collector` per request captures
   queries with their origin, dumps, events and outbound calls. The error page
   shows them together and suggests the likely cause.

3. **No magic.** No container, no facades, no reflection. Everything the CLI
   does is emit readable, committable Go.

## Layout

```
kernel/         boot, modules, pipeline, graceful shutdown
config/         typed configuration, validated at boot
httpx/          router and the mandatory middleware pipeline
security/       Grant and Policy, argon2id, CSRF, sessions
data/           Repository contract (requires a Grant), instrumented DB, migrations
validation/     explicit Validate, no struct tags
observability/  slog, Collector, error page, debug console
modules/auth/   the reference module
```

Dependencies: the standard library and `golang.org/x/crypto`. Nothing else.
Postgres, Redis and NATS arrive through separate adapter modules.

## Repositories

| Repository | Role |
|---|---|
| `arandu-io/framework` | this library |
| `arandu-io/arandu` | project skeleton, what `aru new` clones |
| `arandu-io/aru` | the CLI |
| `arandu-io/docs` | decisions and, from phase D, the guides |

## Status

Phase 1. The exit criteria are in `arandu-io/docs`, `03-roadmap-fases.md`.

## License

To be decided before phase 2.
