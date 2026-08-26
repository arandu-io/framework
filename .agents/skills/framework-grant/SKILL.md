---
name: framework-grant
description: Authorization in the Arandu framework core — security.Grant, what makes it unforgeable, and the tenant that is read off it. Use when changing anything under security/ or data/, when writing or reviewing a method that reaches stored data, when a call will not compile and the missing argument is a Grant, when writing or changing a Policy or a route guard, and when the request mentions "permissions", "roles", "authorize", "who can access", "multi-tenant", "tenant isolation", "scope this query", "SystemGrant", "guest", "this needs a Grant" or "just skip the policy for reads". Also use when tempted to drop the parameter to make something build — here that parameter is the only thing making the query safe. Covers the Policy that issues, data.Tenant, the two fixtures the compiler must refuse, the line a guard must not cross, and the honest limit of the guarantee.
license: MIT
---

# The Grant, and why it cannot be written

`security.Grant` is an alias for `hesape/auth.Grant`, which has only unexported
fields. Nothing outside that package can build one as a struct literal. Every
method that reaches stored data takes one, after the context and before the
identifier:

```go
func (r *UserRepo) Find(ctx context.Context, g security.Grant, id string) (User, error) {
	if err := g.Check(ActionUserView); err != nil {
		return User{}, err
	}
	// The tenant comes from the Grant, never from the request.
	row := r.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE id = ? AND tenant_id = ?`,
		id, data.Tenant(g))
	return scanUser(row)
}
```

That is `modules/auth/user.repo.go:53`, and all nine of that repository's
methods have the same shape:

```sh
grep -c 'g security.Grant' modules/auth/user.repo.go   # 9
```

So a handler that reaches the database without asking a Policy has nothing to
pass. The safe path is not documented; the unsafe path is absent.

## The proof, and it is a compilation

This claim is about the compiler, and the only proof of a claim about the
compiler is an attempted compilation.
`TestRepositoryWithoutGrantDoesNotCompile` (`tests/Feature/data/grant_required_test.go:25`)
runs `go vet` over two fixtures and requires each to fail **with a specific
message**, because a fixture that failed for an unrelated reason would prove
nothing:

| fixture | what it tries | the message required |
| --- | --- | --- |
| `data/testdata/missing_grant` | calling a repository without a Grant | `not enough arguments in call to repo.Find` |
| `data/testdata/forged_grant` | writing `security.Grant{valid: true, ...}` | `cannot refer to unexported field valid` |

Those two programs are why `testdata/` is excluded from the `gofmt` gate. They
are invalid on purpose, and the toolchain skips any directory named `testdata`
when it expands a pattern — which is the only thing keeping code written to fail
out of the build. If you add a fixture, it goes there and the gate must keep
skipping it.

## The procedure

**1. The Policy decides, and it is the only thing that issues.** One policy per
entity, in the module's `<entity>.policy.go`. `modules/auth/user.policy.go` is
the reference:

- The actions are **constants**, not strings at the call site. A typo in an
  action name would silently authorize nothing, or worse, everything.
- **Tenant isolation is the first check and applies to every action**, before
  the switch. Without it every branch below it is pointless.
- The switch has **no default branch that allows**. It denies by falling through.
- Where a guest is allowed anything, the condition is on the **candidate**, not
  on the subject. `s.IsGuest() && len(u.Roles) == 0` is what makes privilege
  escalation through a registration form something that cannot be introduced by
  adding a field to the request struct.

**2. The handler asks, then reads.** `security.Authorize(ctx, policy, subject,
action, resource)` runs the policy and, when allowed, issues the Grant. It is a
wrapper rather than an alias because Go has no alias form for a generic function.

**3. Read the tenant with `data.Tenant(g)`.** Never from a path segment, a body,
a query or a header. The one place a tenant is decided elsewhere is login, where
there is no session to ask yet — `modules/auth.TenantResolver` is that, it is
consulted **only** on login, and its doc says why the asymmetry is the point.
`security.ValidTenant` is what turns a tenant into a namespace safely; it
refuses anything outside `^[a-z0-9][a-z0-9_-]{0,63}$`.

**4. Reads are not exempt.** `List`, `Find`, a read model, a projection, a
report, a dashboard and an export all take a Grant and all filter by the tenant
on it. "The read path can skip the policy" is a cross-tenant leak with a
technical name.

## The line a guard must not cross

A route guard in `http/middleware` answers two questions and no others: is there
a session, and does the subject carry this role. It decides **nothing** about a
record. Whether this subject may read or write *this* row is the Policy's
answer, and the Policy still runs — every handler behind a guard still goes
through its service and therefore through a Grant.

That line is what keeps the Grant the one authorization path. A guard that
starts deciding about records is a second one, and with two of them the one that
gets forgotten is always the guard: a Policy is written once per entity and
reached from everywhere, while a guard is mounted per route and the route
somebody adds next month has none. The argument is written out at
`http/middleware/auth.go:38-54`, in the file somebody reaches for when a Policy
feels like too much work.

`RequireAuth` sends a visitor to `middleware.SignInPath` rather than answering
403, because there is nothing they can do with a 403 and there is something they
can do with the sign-in screen. It remembers where they were going in a signed
cookie rather than in the session, because the session is the thing that does
not exist yet at the moment the guard fires.

## When there is no subject

`security.SystemGrant(action, tenant)` is the named escape hatch: a scheduled
task, a queue worker, a migration runner, and the login path itself, where there
is no subject yet. It is exported and auditable on purpose.

It is a **function and not a var**, deliberately. An exported function variable
holding the framework's one escape hatch is a value any package in the build can
reassign at `init`, and this is the last symbol that should be reachable that
way.

`foundation.Global` says the constraint out loud: a global scheduled task gets
the zero Grant, because `SystemGrant` refuses an empty tenant — so it cannot
pass any `Check` and cannot reach a repository. Global work is cleaning
temporary files, warming a cache, checking a certificate. Work that reads a
customer's rows is `PerTenant`, and having to say so is the point.

## The honest limit

`SystemGrant` exists and is exported, so a handler *can* construct a Grant
nobody authorized. What catches that is a lint in the `aru` CLI, not the type
system — and that distinction is stated on the type rather than hidden, because
the doc comment on `Grant` is where a reader checks the thesis. Everything else
— a repository reachable with no Grant, a tenant chosen by the caller — is a
build that does not complete.

## Where the change goes

`security` and `data` are bridge packages: fifteen of this module's twenty
hold no implementation, and these two are among them. `security` answers to five
hesape packages and `data` to three, each named in its `doc.go`. A change to
what a Grant *does* is a change in `github.com/arandu-io/hesape`; what belongs
here is the alias, the wrapper, or the envelope that translates. Read
`.agents/skills/framework-bridge/SKILL.md` before editing either.

## The line not to cross

If a method will not compile because you have no Grant to pass, the answer is
never to remove the parameter. Ask the caller for one, or use `SystemGrant` with
a tenant if there is genuinely no subject. If neither fits, the design is wrong
rather than the compiler, and saying so is the right output.

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
