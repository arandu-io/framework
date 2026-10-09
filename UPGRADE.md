# Upgrade guide

What changed in a way that stops your code compiling, and what to write instead.

Additions are not listed. A new symbol breaks nothing, and a file that listed
every one of them would be a changelog nobody reads to find the two lines that
matter.

## Before v1.0.0

While the version starts with `v0.`, the API can break. That is what `v0.` means
in Go and it is deliberate — the alternative is freezing a shape before anyone
has built on it. What is not deliberate is breaking it quietly, which is what
this file exists to stop.

Every release from now on is compared against the one before it, across the
whole module, by `apidiff` in CI. An incompatible change that is not written
down here fails the build.

---

## Unreleased — a cookie is `Secure` unless the environment is dev, and the flash takes that one decision

Nothing stops compiling, and `apidiff` reports only the addition of
`Router.Flash`. The entry is here because a cookie attribute a browser acts on
changed.

### `cfg.Session.Secure` defaults to true outside dev

`SESSION_SECURE_COOKIE` still decides when it is set. When it is not, the
default was whether `APP_URL` is https; it is now true in every environment
except `APP_ENV=dev`, and `APP_URL` takes no part. Behind a proxy that ends
TLS the process sees only http, and an `APP_URL` nobody wrote is
`http://localhost:8080`, so the old default left a production session cookie
without the attribute.

- `APP_ENV=staging` or `prod` with an `APP_URL` that is not https and no
  `SESSION_SECURE_COOKIE`: the cookie is now `Secure`. If the browser really
  reaches the application over http, declare `SESSION_SECURE_COOKIE=false`, or
  the session disappears between requests.
- `APP_ENV=dev` with an https `APP_URL` and no `SESSION_SECURE_COOKIE`: the
  cookie is no longer `Secure`. Declare `SESSION_SECURE_COOKIE=true` if the
  development server is served over https and you want it.

An `APP_ENV` nobody wrote is parsed as dev, and answers as dev does.

### The flash cookie is `Secure` exactly when `cfg.Session.Secure` is

The flash the Application builds used to be `Secure` outside dev regardless of
`SESSION_SECURE_COOKIE`. It now takes `cfg.Session.Secure`, so the variable
reaches it: `SESSION_SECURE_COOKIE=false` outside dev drops the attribute from
the flash too, and `SESSION_SECURE_COOKIE=true` in dev sets it.

An application that builds its session store or CSRF issuer from a value of
its own, rather than from `cfg.Session.Secure`, has to give it the same
answer, or the cookies disagree.

A module that writes flash messages of its own takes `r.Flash()` in `Routes`
when it is not nil, instead of building one with `security.NewFlash`.

## v0.52.0 — `CSRFProtect` takes options, and leaves a bearer request with no session to its guard

### `CSRFProtect` is variadic

`apidiff` reports `http/middleware.CSRFProtect` as changed: it gained
`opts ...CSRFOption`. Every call written as
`middleware.CSRFProtect(csrf, sessions.IDFromRequest)` compiles unchanged. What
stops compiling is code that kept the function itself as a value of the old
type — `var protect func(*security.CSRF, func(*http.Request) string) func(http.Handler) http.Handler = middleware.CSRFProtect`.
Call it instead, or give the variable the new type.

The option there is is `CSRFExcept`, for a path a provider posts to with no
session and no token, such as a webhook. The route it exempts has to verify
the request itself, by its signature:

```go
middleware.CSRFProtect(csrf, sessions.IDFromRequest, middleware.CSRFExcept("/webhooks/"))
```

### A bearer request with no session is not asked for a CSRF token

A write that carries `Authorization: Bearer` and no valid session cookie is now
passed to its route without a CSRF token, and `RequireToken` authenticates it
or answers 401. A route that relied on `CSRFProtect` to refuse such a request
has to be behind a guard: one with no guard at all now takes the write. Basic,
Digest and Negotiate are still checked, the origin check still applies, and a
request with a valid session cookie is checked exactly as before.

## Unreleased — an API request authenticates by bearer token, a write can be replayed by its Idempotency-Key, and an action's error is answered by the one status table, as JSON when JSON was asked for

This release requires `hesape` v0.50.1. Nothing stops compiling, and `apidiff`
reports only additions. The entry is here because an application that wrote
any of these by hand has code to delete, and because the answers a client
receives are part of the contract.

### `RequireToken` carries the subject of a bearer token

`middleware.RequireToken(tokens)` reads `Authorization: Bearer`, hands the
application's `TokenResolver` the SHA-256 of the token — a `TokenDigest`, never
the token — and puts the subject it returns on the request context, where
`ctx.User()` reads it exactly as it does behind `RequireAuth`. The tenant is the
subject's; nothing on the request names it.

Issuing, storing, scoping, expiring and revoking tokens stay the
application's. What it stores is `middleware.DigestToken(token).String()`, and
`ResolveToken` looks the digest up and answers the subject, or
`middleware.ErrUnknownToken`:

```go
func (r TokenRepo) ResolveToken(ctx context.Context, d middleware.TokenDigest) (security.Subject, error) {
	// SELECT user_id, tenant_id, actions FROM api_tokens
	//  WHERE digest = $1 AND revoked_at IS NULL AND expires_at > now()
	// no row: return security.Subject{}, middleware.ErrUnknownToken
}

api := r.Group("/api", middleware.RequireToken(tokens))
```

What to check:

- **A token stored in the clear has to be re-stored as its digest**, or
  re-issued. The resolver is never handed the token, so a lookup by the token
  finds nothing and every request is answered 401.
- **A missing token and an unknown one are the same 401**, with
  `WWW-Authenticate: Bearer` and the same body; a client that wants JSON gets a
  problem document. A resolver error other than `ErrUnknownToken` is not a 401:
  it panics, and the recover middleware answers 500.
- **The token is the only credential the route takes.** There is no fallback to
  the session cookie, and a subject already on the request is replaced by the
  token's rather than kept, so a cookie cannot widen what a token may do.
- An application helper that parsed the header and called its own token table
  can go, along with the `actor()` that read the subject back.

### `Idempotent` replays a write by its `Idempotency-Key`

`middleware.Idempotent(store, ttl)` runs the first `POST`, `PUT`, `PATCH` or
`DELETE` carrying a key and stores its answer — status, the representation
headers (never `Set-Cookie`), body — for `ttl`. A retry with the same key and
the same request gets that answer with `Idempotent-Replayed: true` and does not
run. The same key on another body, method or address is 422; a copy that
arrives while the first is still running is 409 with `Retry-After`; a malformed
key is 400. An answer of 400 or above is not kept, so its retry runs.

The key is scoped to the tenant and the id of the subject on the context, so it
is mounted **after** the guard:

```go
store := cache.NewDatabaseStore(...) // or the RESP store: shared by every process
r.Post("/api/charges", charges.Store,
	middleware.RequireToken(tokens),
	middleware.Idempotent(store, 24*time.Hour))
```

What to check:

- **A request with a key and no authenticated subject panics**, naming the fix.
  Mounted before the guard, or on a public route, the middleware has nobody to
  scope the key to, and it refuses rather than run the write unprotected.
- **The store must be shared by every process that serves the route.**
  `*cache.ArrayStore` keeps keys in one process's memory, and the next process
  runs the retry again. A store that cannot be read before the handler runs is
  a panic and a 500, never a write run anyway.
- The body is read in full before the handler runs and handed to it unchanged;
  a limit mounted earlier still applies and is answered 413.

### An action's error is read by `exception.StatusOf`, and a status the error declares wins

The action adapter behind `Router.Action` and `Router.Resource` read a table of
its own, which matched the sentinels before an error's `HTTPStatus()`. It now
reads `hesape/exception.StatusOf`, the table `exception.Handler` reads too, so
an error is answered with the same status wherever it surfaces. The rows are
the ones it had; the order is not. A status the error declares is the explicit
statement and wins over a sentinel it wraps, which may be only its cause:

| error an action returns | before | now |
| --- | --- | --- |
| `&exception.HTTPError{Status: 404, Err: security.ErrForbidden}` | 403 | 404 |
| a type of your own with `HTTPStatus() int` wrapping `database.ErrRecordNotFound` | 404 | its own status |
| a failed `Validate`, a `*validation.ValidationException` | panic, 500 | the flash and the redirect back, as `validation.Errors` |

The answer now also carries the headers the error asks for: those of the first
error in the chain with a method `GetHeaders() http.Header`, matched by method
set as `HTTPStatus()` is, on a page and in a problem document alike. The errors
of `hesape/http/exceptions` answer it, so a `ThrottleRequestsException` goes out
with the `Retry-After` the adapter used to drop, and an application's own error
declares headers the same way.

What to check:

- **An error type whose `HTTPStatus()` was written as a fallback**, counting on
  the sentinel it wraps to decide the answer. Return the sentinel, or declare
  the status you mean.
- **The `Headers` of an `*exception.HTTPError` now reach the response**:
  `hesape` v0.50.1 gave that type `GetHeaders`, so an action that returns one
  with `Retry-After` sends it, like a `ThrottleRequestsException` or a type of
  your own with both methods.

### A request that wants JSON gets a problem document

An action's error is now answered in the representation the request asked for.
`Context.WantsJSON` decides it: `Accept` names `application/json`, or the
request is an XHR that is not htmx. It is the default rule the exception
handler already applied to a failure that never reached an action; an
`exception.Config.RenderJSONWhen` an application set is not read here.

| error an action returns | a page, unchanged | a request that wants JSON, before | now |
| --- | --- | --- | --- |
| `validation.Errors`, or a failed `Validate` | flash, 303 back | flash, 303 back | 422 `application/problem+json`, the messages in `errors` keyed by field |
| an error `exception.StatusOf` claims | `Refuse`: the status and a sentence | the same, as `text/plain` | the status as `application/problem+json` |

The problem is written by `exception.WriteProblem` and
`exception.WriteValidationProblem`, with `Cache-Control: no-store, private`, the
request id and the path without its query. The sentence in `detail` is the one
a page shows, never the error's own text.

What to check:

- **A JSON client that followed the 303** and read the flash, or parsed the
  `text/plain` body of a refusal, reads the problem instead. It receives no
  `Location` and no flash cookie.
- **An htmx request is answered as a page** whatever it accepts, because it
  swaps HTML.
- **A JSON client no longer needs the flash**: on a router without `WithFlash`
  its rejection is answered 422. A page's rejection there still panics.
- **An application's own problem writer for these errors can go**, along with
  the `if ctx.WantsJSON()` branch in front of it.

### A dot in a `Router.Resource` name nests the resource

`Router.Resource` hands its name to `routing.Resource`, which read it as one
path segment, dots included, and now reads a dot as nesting, shallow: the list,
the form and the store under the parent, the four that act on one record at the
record's own path, every route still named with the whole dotted name. Each
parameter of a nested resource is the singular of its segment.

| call | before | now |
| --- | --- | --- |
| `r.Resource("projects.tasks", c)`, the list | `GET /projects.tasks` | `GET /projects/{project}/tasks` |
| `r.Resource("projects.tasks", c)`, one record | `GET /projects.tasks/{id}` | `GET /tasks/{task}` |
| `r.Resource("invoices", c)` | `GET /invoices/{id}` | unchanged |

What to check: a controller registered under a dotted name reads `{task}` where
it read `{id}`, and loads the parent named by `{project}` under its Grant. To
keep a dot as a literal segment, register the routes one by one with `Action`.

### `events.Module` hands over hesape's outbox migrations

`events.Module.Migrations` returns the two migrations `hesape/events.Module`
declares, under the names this module has always used —
`2026_07_31_000001_create_outbox_table` and
`2026_07_31_000002_add_outbox_dead_letter` — so a database that ran them does
not run them again. It used to declare a copy of each: two definitions of one
table under one name, which the migration registry refuses as a copied file
when a project registers both modules' lists.

What to check: nothing for a project that registers this module alone. One
that also registers `hesape/events.Module`'s migrations now gets each name once
instead of a panic.

## v0.50.0 — the guards carry the subject, an action's error is answered with its status, a guest's CSRF token is bound to the guest, and the rate limit asks the store

This release requires `hesape` v0.44.0. One signature changes and stops
compiling — `middleware.KeyBySession` — and `apidiff` reports it. The rest is
written down because what a handler sees, and what a client receives, changes.

### `KeyBySession` takes the session store, and keys only a live session

`http/middleware.KeyBySession` took a function returning the session id the
cookie named. A signature on the cookie proves only that the id was issued here
once, so every expired or signed-out id a client kept was a fresh rate-limit
budget. It now takes a `middleware.Sessions` — `IDFromRequest` and `Load`, which
`*security.SessionStore` already has — and keys by the session only while the
store holds it, falling back to the address otherwise.

```go
middleware.KeyBySession(sessions.IDFromRequest) // before: no longer compiles
middleware.KeyBySession(sessions)               // now
```

The key a live session is counted under is the same string as before, so no
counter is reset on deploy. Each keyed request now reads the session from the
store once.

### A guest's CSRF token is bound to the guest, and `CSRFProtect` issues it

A visitor with no session was issued a token bound to the empty id — one token
for every visitor, valid on anybody's sign-in form for its whole lifetime.
`hesape` v0.44.0 refuses that binding: `CSRF.Issue("")` returns
`session.ErrUnboundToken`, and a token is validated only against a session id or
a guest binding.

`CSRFProtect` now does the issuing. On every `GET` and `HEAD` it issues a token
for the request's binding — the session id, or for a visitor without one a
random id in the signed `arandu_csrf_guest` cookie (HttpOnly, SameSite=Lax,
Secure), set on their first page — and puts it on the request context.
`view.New` reads it into `Page.Token`. On a write that passed the check, the
token that was submitted is put there instead, so a form drawn again on the
same request carries a token that still validates. The signature of
`CSRFProtect` is unchanged.

What to check:

- **A controller that issued the token itself** with
  `csrf.Issue(sessions.IDFromRequest(ctx.Request))` gets `ErrUnboundToken` for
  every visitor without a session, and the sign-in screen answers 500. Delete
  the issuing and the `.WithToken(token)`: `view.New(ctx, title)` already
  carries the token. `WithToken` remains for a page drawn outside
  `CSRFProtect`, and such a page issues with
  `csrf.Issue(csrf.Binding(w, r, sessions.IDFromRequest(r)))`.
- **Development over plain HTTP** builds the issuer with
  `security.NewCSRF(key, ttl).Secure(false)`. Without it the browser never sends
  the guest cookie back, and every form a guest submits answers 419.
- **A page that carries a token is a page for one visitor.** A shared cache
  that served one visitor's page to another now serves a token bound to
  somebody else, and that form answers 419. Before, the token was the same for
  every guest, which is what made the cache look harmless.
- A guest's first page sets a cookie. A response that must set none is one
  that sits outside `CSRFProtect`.
- A request carrying neither a session cookie nor the guest cookie — a client
  that drops cookies — has nothing to bind to, and every write it sends is
  answered 419, header token or not.

`modules/auth` draws its sign-in screen with the token from the request
context, and issues one for `CSRF.Binding` only where `CSRFProtect` did not run.

### The route guards put the subject on the request context

`RequireAuth`, `RequireRole` and `RequireConfirmedPassword` now put the subject
they loaded from the session on the request context, so `ctx.User()` answers it
behind them. The new `middleware.LoadSubject(sessions)` does the same on a
public route: it carries the subject when a valid session exists and lets every
request through, with no redirect and no subject when there is none. A handler
that loaded the session again only to learn who is asking reads `ctx.User()`
instead:

```go
subject, err := c.sessions.Load(ctx.Ctx(), ctx.Request)   // before
subject, ok := ctx.User()                                  // now
```

The `Grant` is never on the context; it still comes from a Policy. A missing or
expired session is answered exactly as before.

### An action's error is answered with its status

An action registered with `Router.Action` or `Router.Resource` that returns one
of these errors, bare or wrapped with `%w`, is now answered with the status
through `Refuse` instead of panicking:

| error | status |
| --- | --- |
| `model.ErrModelNotFound`, `database.ErrRecordNotFound` (`hesape`) | 404 |
| `security.ErrForbidden` | 403 |
| `security.ErrCSRF` | 419 |
| `database.ErrUniqueViolation` (`hesape`) | 409 |
| an error with a method `HTTPStatus() int`, returning 400–599 | that status |

A duplicate key is classified by the driver's own error code, never by the
message. A body cut off by the size limit is answered 413 through the last row:
`Bind` and `Validate` return a `PostTooLargeException` that declares it.

The first match in that order wins. A whole page gets the status and the
standard sentence for it; an htmx request gets the status and `HX-Refresh`, the
same answer the route guards and CSRF give. The error's own text is never
shown. A declared status outside 400–599 panics, naming the type.

What to check:

- **These errors no longer reach the recover middleware.** They are not logged
  as a recovered panic, and they do not draw the application's own
  `errors/<status>` view: the answer is the refusal, not the status page. A
  project that relied on either for these errors handles it in the action.
- **A `fail()` helper that mapped these errors by hand can go**, and a domain
  failure declares its status instead of being switched on per controller:

  ```go
  type InvoiceLocked struct{}

  func (InvoiceLocked) Error() string   { return "invoice is locked" }
  func (InvoiceLocked) HTTPStatus() int { return http.StatusConflict }
  ```

- **A duplicate key on a write an action returns is 409, no longer 500.** It is
  not logged as a recovered panic. A form that should say which field is taken
  still checks first and returns `validation.Errors`; the 409 is what is left
  for the race between that check and the insert.
- `validation.Errors` is answered exactly as before, and any other error still
  panics and is answered 500.

### CSRF removes the multipart files it parsed

`CSRFProtect` reads the `X-CSRF-Token` header first and the `_token` form field
after it, as before. When the token comes from a multipart body, it parses the form and
removes its temporary files once the handler returns, whatever the answer. Until
now those files were never removed: the request reaching the middleware is a
copy, and net/http cleans up only after the one it created, so every multipart
POST that reached it — a refused one included — left whatever did not fit in
32 MB on disk.

What to check:

- **A handler that keeps an upload past its return** copies the file somewhere
  first. A form parsed by `CSRFProtect` is gone once the handler has returned.
- **A request whose token is in the header reaches the handler with its body
  unread**, as before. A handler behind it that calls `FormFile`,
  `ParseMultipartForm` or `PostFormValue` on a multipart body removes the form
  itself, `defer r.MultipartForm.RemoveAll()`, for the same reason.
- The legacy sign-in handler of `modules/auth` reads its form with `ParseForm`,
  which does not parse a multipart body: a multipart sign-in carrying the token
  in the header now arrives with no fields and is answered 422.

### The framework's own routes carry the security headers

Every route under `/_arandu/` — the two probes, the debug console, the
development reload and the asset route — skips the application's pipeline, and
so skipped the `SecurityHeaders` an application mounts there: it went out with
no `Content-Security-Policy`, no `X-Content-Type-Options` and no
`X-Frame-Options`. The `Application` now answers those routes with the default
headers itself, `Strict-Transport-Security` outside development included.

What to check:

- The policy on those routes is the default one. The image origins an
  application passes to `SecurityHeaders` do not reach them, because nothing
  the framework serves embeds an image from elsewhere.
- The debug console's policy adds `'unsafe-inline'` to `style-src`, and nothing
  else: its pages carry their styles inline.
- An asset served from `/_arandu/` is now answered with
  `Cross-Origin-Resource-Policy: same-origin`. A page on another origin that
  loaded one directly is refused it; serve the asset from the application that
  renders the page.

## v0.49.0 — `SecurityHeaders` takes the origins an image may load from

`middleware.SecurityHeaders(dev bool)` is now
`middleware.SecurityHeaders(dev bool, imageOrigins ...string)`, on `hesape`
v0.43.0, which this release requires. Every call compiles unchanged and answers
the same policy, byte for byte. `apidiff` reports the change because the type of
the function changed: **only code that stores `SecurityHeaders` in a variable of
type `func(bool) func(http.Handler) http.Handler` breaks** — give the variable
the new type.

The origins reach `img-src` and no other directive, and each must be a bare
`https` origin; anything else panics while the pipeline is wired:

```go
middleware.SecurityHeaders(cfg.App.IsDev(), "https://cdn.example.com")
// img-src 'self' data: https://cdn.example.com
```

It exists because a disk with a public address (`filesystem.Config.URL`,
`Disk.URL`) is how a bucket behind a CDN hands out its files, and the default
policy refused to draw the address `Disk.URL` returns.

## v0.42.0 — the CSRF form field is read as `_token`

`CSRFProtect` read the hidden field as `_csrf`. The form builder in `hesape`
wrote `_token`, and the session has always stored the token under that key, so a
form built rather than hand-written came back 419 with a message telling the
developer to add a field the form already had.

One spelling survives, and it is `_token`. The `X-CSRF-Token` header did not
move, so a request that sends the token as a header is unaffected.

A view that uses the `@csrf` directive needs no change — the directive writes
the new spelling as of `hesape` v0.21.0, which this release requires. A form or
an HTMX request that writes the field by hand does:

```html
<input type="hidden" name="_csrf" value="{{ .CSRFToken }}">   <!-- before -->
<input type="hidden" name="_token" value="{{ .CSRFToken }}">  <!-- now -->
```

The refusal message changed with it. It named the spelling it was about to stop
reading, which is the first thing a person sees when a form fails.

`apidiff` reports nothing here: the name of a form field is a string literal
inside a function body, not part of the exported surface. This entry is the
record. A public integration test in this module renders Hesape's real
`FormBuilder`, derives the browser submission from that HTML and crosses
`CSRFProtect → OverrideMethod → handler`; it fails if either side changes the
field or if the middleware order changes.

---

## Unreleased — the components move out, and `httpx` becomes `http`

Four changes, and the first two are import paths rather than behaviour. Only the
last renames anything: `Grant`, `Context`, `Router` and `Module` answer to what
they always did, and `Migration` is the one that does not.

### `httpx` is `http`

Every import of `github.com/arandu-io/framework/httpx` becomes
`github.com/arandu-io/framework/http`, and `httpx/middleware` moves with it. The
`x` was never a convention — it marked that `net/http` had the word first, and a
package is named for what it holds, not for what took the name first (ADR 0047).

A file that imports both aliases **ours**, so `http` goes on meaning `net/http`
as it does in every other Go file:

```go
import (
	"net/http"

	fhttp "github.com/arandu-io/framework/http"
)

func (c *InvoiceController) Index(ctx *fhttp.Context) error
```

A file that does not import `net/http` needs no alias and reads `http.Context`.

No shim is left behind. `framework/httpx` does not exist, and an import of it
fails to resolve rather than compiling against something stale.

### `kernel` is `foundation`, and `Kernel` is `Application`

The type is the application object, and the name now says so: `kernel.Kernel` is
`foundation.Application` (ADR 0049). Unlike the components, `foundation` is not a
module of its own: it ships only inside this one.

`framework/kernel` still works. It is a bridge, and it is removed in v1.0.0 —
every method keeps its name, so the change is the import path and the type name:

```go
app := foundation.New(cfg)   // was kernel.New(cfg), and still is
```

Two symbols did not survive the move, and neither was reachable from an
application: `Locker` moved down into `foundation` under the same name, and
`FormatRoutes` now calls through to `hesape/routing`.

### The application is built from one struct per component

| was | is | what to do |
|---|---|---|
| `kernel.New(config.Config)` | `kernel.New(bootstrap.Configuration)` | Build it with `bootstrap.LoadConfiguration()` and pass the result. `foundation.New` and `(*Application).Config()` follow it |

`config.Config` was one struct of eleven fields read by one function. What
replaces it is `github.com/arandu-io/framework/foundation/bootstrap`, where each
component declares its own settings and `LoadConfiguration` reads the
environment once to fill them in:

```go
cfg, err := bootstrap.LoadConfiguration()
if err != nil {
	return err
}
app := foundation.New(cfg)
```

The fields an application reaches for most:

| was | is |
|---|---|
| `cfg.AppName`, `cfg.Env`, `cfg.HTTPAddr`, `cfg.AppKey` | `cfg.App.Name`, `cfg.App.Env`, `cfg.App.HTTPAddr`, `cfg.App.Key` |
| `cfg.IsDev()` | `cfg.App.Env.Is(config.EnvDev)`, with `github.com/arandu-io/hesape/config` |
| `cfg.SessionTTL` | `cfg.Session.Lifetime` |
| `cfg.Database` | `cfg.Database`, which is `hesape/database.Config` |
| `cfg.LogLevel`, `cfg.TracingSecret`, `cfg.Editor` | `cfg.Observability.LogLevel`, `.TracingSecret`, `.Editor` |

`framework/config` still loads and still validates. It is a bridge from here,
removed in v1.0.0, and nothing in the framework reads it any more.

Two settings have no field yet, and an application that reads `RedisURL` or
`CSRFTTL` off `config.Config` keeps doing so until they do.

One behaviour changed with the move: `LOG_LEVEL` is parsed at boot, so a name
outside the eight the logger knows stops the process instead of restoring a
default. `warn` is spelled `warning`.

Three constants went with it, and none of them was reachable from anything:

| was | is | what to do |
|---|---|---|
| `config.AppKeyLen` | `encryption.KeySize`, in `github.com/arandu-io/hesape/encryption` | It said 32 twice. The key is parsed and validated by `encryption`, which is where the length belongs |
| `config.DefaultSQLitePath` | `database.DefaultSQLitePath`, in `github.com/arandu-io/hesape/database` | Same value, one owner |
| `config.DefaultDatabaseURL` | `database.DefaultURL`, same package | Same |

### The components are their own module

`github.com/arandu-io/hesape` is now a `require` of this one. Nothing in your
code has to import it: every package you already use is still here, as a thin
bridge over the one that answers for it, and every bridge names its replacement
and the release it disappears in.

Where a name changed on the way down, the bridge translates rather than exposing
the new one — `security.SessionStore` still has `Load`, `Rotate`, `Destroy` and
`IDFromRequest`, though `hesape/session` calls them `All`, `Regenerate`,
`Invalidate` and `ID`.

One signature could not be preserved:

| was | is | what to do |
|---|---|---|
| `subject.PasswordConfirmedWithin(d)` | `security.PasswordConfirmedWithin(subject, d)` | It was a method on `Subject`, and `Subject` is now an alias for `auth.Subject` — Go forbids declaring a method on another package's type |

### A migration is a type, not three strings

`Migration` was `{ID, Up, Down string}` and is now an interface (ADR 0063): `ID`
is `GetName()`, and `Up` and `Down` are methods that take a connection.

```go
type CreateInvoicesTable struct{ migrations.BaseMigration }

func (CreateInvoicesTable) GetName() string {
	return "2026_08_17_000001_create_invoices_table"
}

func (CreateInvoicesTable) Up(ctx context.Context, conn migrations.Connection) error {
	_, err := conn.Statement(ctx, `CREATE TABLE invoices (...)`, nil)
	return err
}

func (CreateInvoicesTable) Down(ctx context.Context, conn migrations.Connection) error {
	_, err := conn.Statement(ctx, `DROP TABLE invoices`, nil)
	return err
}
```

`BaseMigration` answers `GetConnection`, `ShouldRun` and `WithinTransaction`, so
only `GetName` and `Up` are left to write. `Down` is optional and tested for by
type assertion, which makes a `Down` with the wrong signature a build failure
rather than a rollback that silently does nothing.

**Keep the id.** It becomes the string `GetName` returns, unchanged. A migration
that has run in a database and comes back under a different name is a migration
that runs again.

**One statement per call.** The `Up` string used to be split on semicolons before
it was sent. Nothing splits now, so a body that held three statements becomes
three `conn.Statement` calls, in order.

The runner went with the struct:

| was | is | what to do |
|---|---|---|
| `data.Migrate(ctx, *DB, []Migration)` | `(*migrations.Migrator).Run` / `.RunPending` | Build one with `migrations.NewMigrator`, and adapt the connection with `database.ForMigrations` or `database.MigrationResolver` |
| `data.Rollback(ctx, *DB, []Migration)` | `(*migrations.Migrator).Rollback` | `Options.Steps` and `Options.Batch` decide how far back, and neither existed before |
| `data.Status(ctx, *DB, []Migration)` | `(migrations.MigrationRepositoryInterface).GetMigrationBatches` | It answers name to batch, which is the table `migrate:status` prints |
| `data.AppliedMigrations(ctx, *DB)` | `(migrations.MigrationRepositoryInterface).GetRan` | Names only, ordered by batch and then by name. For the batch as well, `GetMigrationBatches` |
| `data.Pending(ctx, *DB, []Migration)` | `(*migrations.Migrator).Run` | Pending is computed inside `Run` now, against the registry, so there is nothing left to ask separately |
| `data.AppliedMigration` | `migrations.MigrationRecord` | The fields are `ID`, `Migration` and `Batch` |
| `data.MigrationsTable` | `migrations.DefaultTable` | **The value changed**, from `arandu_migrations` to `migrations`. A database migrated by an older binary has its record under the old name: pass `arandu_migrations` to `migrations.NewDatabaseMigrationRepository` to keep reading it, or rename the table |

`data.Migration` and `data.KeyText` stay, and `data.Migration` is now an alias
for `migrations.Migration`. Everything else in `data/migrate.go` is gone rather
than deprecated: the migrator takes a connection instead of a `*DB`, so there was
no signature left to bridge.

A module still declares its schema with `Migrations() []kernel.Migration`, and
the Application still collects them in registration order. What is new beside it
is `migrations.Register` from an `init()`, which is how an application's own
migrations reach the `Migrator` — a package nothing calls cannot be asked.

### The root package is gone, and it never held anything

`apidiff` reports `github.com/arandu-io/framework: removed`, and it is right
about the fact and misleading about the consequence, so it is written down here
rather than argued with.

That package existed only because two test files sat at the top of the
repository. It had no non-test file, therefore no exported symbol, and
therefore could never be imported: `import "github.com/arandu-io/framework"`
answered `no Go files in ...` at v0.32.0 exactly as it does now. The two tests
moved into `tests/`, the last file at the root went with them, and the package
stopped existing.

**Nothing to change.** No code could have named it.

What is importable and new beside it is `github.com/arandu-io/framework/tests`,
which is the base the suites build on. It exports `ModuleRoot`, it imports
`testing`, and no code that ships should reach it.

---

## v0.41.0 — configuration fails closed, and rate limits are shared

Configuration that would expire a credential as it is issued or defer a broken
Redis address until the first request now stops the process during `config.Load`
or `Config.Validate`. An absent optional value still keeps its documented
default; only an explicitly unusable value is refused.

### TTLs are positive whole seconds

`SESSION_TTL` and `CSRF_TTL` are counts of seconds. When set, each must be a
positive whole number that fits in a `time.Duration`. Text such as `12h`, zero,
negative values and overflow now return an error instead of silently falling
back to 12 hours for sessions or 2 hours for CSRF tokens.

Remove the variable to keep the default, or write the duration in seconds:

```dotenv
SESSION_TTL=43200
CSRF_TTL=7200
```

Code that constructs `config.Config` directly is checked by `Config.Validate`
as well: both `SessionTTL` and `CSRFTTL` must be greater than zero.

### A configured Redis URL must name a Redis host

An empty `REDIS_URL` remains valid for applications that do not select a
Redis-backed store. When it is set, it must parse as a URL, use `redis://` or
`rediss://`, and name a host. Invalid values fail during startup, and the error
does not repeat the URL or any credential it carries.

### The rate limit counts in a store, not in this process

`apidiff` reports four removals from `http/middleware`:

```
- ./http/middleware.Limiter: removed
- ./http/middleware.MemoryLimiter: removed
- ./http/middleware.NewMemoryLimiter: removed
- ./http/middleware.RateLimit: removed
```

`MemoryLimiter` counted in process memory, so `N` replicas allowed `N` times the
limit — on the endpoints a limit is put there for. `Limiter` was the interface it
was meant to be swapped through, and the distributed implementation it named
never existed: `MemoryLimiter` was the only type in the collection with that
method, so the interface stood in front of one implementation declared beside it.

The limit is now `hesape/routing/middleware.Throttle` over a
`hesape/cache.RateLimiter`, which counts in a `cache.Store`. Two replicas over
one store are one budget.

`KeyByIP` and `KeyBySession` stay here, unchanged and byte-for-byte compatible.
A counter in a shared store is keyed by the string they return, so they are the
one part of this that must not move.

Where the wiring read:

```go
limiter := middleware.NewMemoryLimiter()

app.Use(
	middleware.RateLimit(limiter, 300, time.Minute, middleware.KeyBySession(sessions.IDFromRequest)),
)
```

it becomes:

```go
import (
	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/http/middleware"
	"github.com/arandu-io/hesape/cache"
	hmiddleware "github.com/arandu-io/hesape/routing/middleware"
)

limiter := cache.NewRateLimiter(store)

app.Use(
	hmiddleware.Throttle(limiter, cache.PerMinute(300),
		middleware.KeyBySession(sessions.IDFromRequest), fhttp.Refuse),
)
```

Four things changed and each is doing work:

- **`store`** is the `cache.Store` the counter lives in. `cache.NewArrayStore()`
  is in process and behaves exactly as `MemoryLimiter` did, which makes it the
  honest choice for one instance and the wrong one for two;
  `redis.NewRedisStore(conn)` is the shared one.
- **`cache.PerMinute(300)`** replaces the `300, time.Minute` pair. The budget and
  the window are one value now, which is what a named limiter registered with
  `RateLimiter.For` resolves to.
- **`fhttp.Refuse`** is passed rather than assumed. `Throttle` takes the refusal
  as a parameter because how a 4xx is written belongs to the request layer — this
  one adds `HX-Refresh`, without which a person over the limit presses the button
  and the screen does not change.
- **The store can fail, and `Allow` could not say so.** `Throttle` lets the
  request through and logs at `ERROR`: a rate limiter that is down must not
  become an outage. A caller whose budget is the security control rather than a
  guard against volume — a sign-in — checks in the handler against the same
  limiter, where it can fail closed on the same error.

`Throttle` also panics when it is built with a limit no request could satisfy —
`PerMinute(0)`, a zero window, a nil limiter or key. That case used to be a route
that silently carried no limit at all.

---

## v0.13.3 — everything published so far

The entries below were measured, not remembered: `apidiff` between `v0.1.0` and
`v0.13.3`, over every package of the module.

They are grouped by package rather than by version. Per-version attribution was
not reconstructed, and saying so is better than guessing at it — the twenty-four
tags predate this file, and from here the CI step produces the attribution as
each release happens.

### `data`

| was | is | what to do |
|---|---|---|
| `Wrap(*sql.DB) *DB` | `Wrap(*sql.DB, Dialect) *DB` | Pass the dialect. `data.ParseDialect(cfg.Database.Connection)` reads it from configuration |
| `AppliedMigrations(ctx, *DB) (map[string]bool, error)` | `AppliedMigrations(ctx, *DB) ([]AppliedMigration, error)` | The map answered "did it run"; the slice also carries the batch, which is what makes `Rollback` undo a deploy rather than one file |
| `Query.Filter` | *removed* | It never filtered anything: no producer set it and no consumer read it. A field that silently does not work is worse than no field, and implementing it would have been a query builder, which RULE 9 refuses. Write the condition in the repository method |

### `httpx`

| was | is | what to do |
|---|---|---|
| `(*Router).Get/Post/Put/Patch/Delete` returned nothing | they return `*Route` | Nothing, unless you assigned the result. The return is what makes `.Name("home")` chain |
| `(*Router).Routes() []Route` | `() []*Route` | A route is now addressable after registration, so it is handed out by pointer |
| `Route.Name` (a field) | `(*Route).Name(string)` sets it, `(*Route).RouteName()` reads it | Reading `r.Name` becomes `r.RouteName()`. The field never held anything: it existed and was never filled |

### `httpx/middleware`

| was | is | what to do |
|---|---|---|
| `Observe(bool, string)` | `Observe(bool, string, *observability.Recorder)` | Pass `k.Recorder()`. Passing `nil` records nothing, which is what production does |

### `observability`

| was | is | what to do |
|---|---|---|
| `Collector.Dumps/Events/External/Queries` (fields) | methods with the same names | Add `()`. They became methods when the Collector started being read from more than one goroutine |
| `HandleDebugConsole` | `NewConsole(...)`, mounted at `ConsolePath` | The console is a module now, registered like any other, rather than a handler you wire by hand |

### `kernel`

| was | is | what to do |
|---|---|---|
| `(*Kernel).Routes() []httpx.Route` | `() []*httpx.Route` | Follows the router change above |
| `FormatRoutes([]httpx.Route)` | `([]*httpx.Route)` | Same |

### `config`

| was | is | what to do |
|---|---|---|
| `Config.DatabaseURL string` | `Config.Database DatabaseConfig` | One URL could not express a pool size, a connection lifetime or a dialect. The `.env` keys are the conventional `DB_*` ones |

### `modules/auth`

| was | is | what to do |
|---|---|---|
| `New(*Service) *Module` | `New(*Service, TenantResolver) *Module` | Pass `auth.FixedTenant(cfg.Auth.Tenant)` for a single-tenant application. A resolver reading the host name is the multi-tenant version, and it is the same call |
| `Module` was comparable | it is not | If you compared two `Module` values with `==`, compare their names |

---

## v0.16.0

Nothing broke. Two additions worth knowing about, because they change what is
possible rather than what compiles:

- `security.Guest(tenant)` is an anonymous reader, declared on purpose, and
  `Authorize` lets it reach the policy. A `Subject` nobody filled in is still
  refused before the policy is consulted — the marker is unexported and only
  `Guest` sets it, so a forgotten session load cannot borrow the public path. A
  policy that says nothing about guests denies them, which is what every
  generated policy does. See ADR 0029.
- `(*httpx.Context).URL(name, params...)` builds the path of a named route. The
  table was already there and reachable only from the router, so every
  controller that wanted a link built one by hand.

## Deprecation

Nothing is deprecated right now, and that is a statement rather than an
omission: the list above is what has already broken, and there is no symbol
currently on its way out.

When one is, it goes through four steps and this file records each:

1. `// Deprecated:` on the symbol, naming what replaces it
2. a warning from `aru doctor`, not an error
3. an entry here, with the before and after
4. removal, never in the same release as step 1

## How this is checked

`apidiff` runs in CI on every pull request, comparing the working tree against
the latest tag, across the whole module. Comparing it package by package would
miss the worst break of all: a package that was deleted is no longer in the list
of packages, so nothing asks about it. Incompatible changes are printed in the
build log, and the build fails when there are some and this file has no entry
for them.

The point is not to prevent the break. It is to make the break a thing somebody
decided, in a diff a reviewer can see, instead of something a person discovers
when their build stops.
