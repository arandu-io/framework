// Package view is the view layer: kyse for markup, HTMX for interaction,
// Alpine for ephemeral client state, Tailwind for style. It is a binary and it
// is never Node.
//
// A project that uses it still runs with `git clone && aru dev`: no
// node_modules, no package.json, no lockfile of JavaScript, nothing installed
// beyond Go and the standalone binaries the CLI fetches. Having a build step is
// allowed; being Node is not (RULE 13).
//
// It lives in the framework and the views do not, and that split is deliberate:
// resources/views/ belongs to the project, because it is edited; the rendering
// machinery belongs here, because it is not. It used to be a repository of its
// own, and dissolving it is ADR 0021.
//
// The error page deliberately does not use this package. It has to render when
// the rest is broken, including when the view build failed, so it stays as
// html/template inline in observability/errorpage.
//
// This package is a bridge. It is removed in v1.0.0; import github.com/arandu-io/hesape/view directly.
//
// The components moved to github.com/arandu-io/hesape, under the Illuminate
// names, and this package is now the old names pointing at them. Everything the
// view runtime is made of answers to one hesape package:
//
//	hesape/view  the compiled-view registry, the runtime generated code calls,
//	             the served assets, and the development reload script
//
// The death date above is what keeps this from being a second way to import one
// type, which RULE 9 forbids. Bridging the registries is not only a matter of
// tidiness: Register, RegisterLayout and RegisterAsset write into package-level
// tables, and a framework that kept its own would have generated views landing
// in one table while the Renderer the kernel installed read the other.
//
// # What is NOT bridged, and why
//
// Two files here still hold an implementation, because the hesape design
// diverged in a way no envelope can absorb without breaking a caller:
//
//	Page, Layout, New  hesape/view renamed the four error accessors after
//	                   Illuminate's MessageBag -- FieldError became First,
//	                   FieldErrors became Get, HasErrors became Any and
//	                   ErrorSummary became All, and the Layout interface
//	                   followed. github.com/arandu-io/kyse declares a
//	                   two-method interface asking for FieldError, in a
//	                   separate module, so an alias compiles here and breaks
//	                   the component library in silence. An envelope cannot
//	                   stand in either: Page is written as a composite literal
//	                   across the skeleton and the published screens, and
//	                   promoted fields are not addressable in one. hesape's New
//	                   also takes a *hesape/http.Context and its Errors field
//	                   is a hesape/validation.Errors, neither of which this
//	                   module's httpx and validation reach yet.
//	Module             hesape/view.Module takes a *hesape/routing.Router and
//	                   answers a hesape/http.Renderer, and it deliberately
//	                   drops the compile-time assertion against the module
//	                   contract while foundation is still being built. This one
//	                   keeps kernel.Module and *httpx.Router, and hands over
//	                   the bridged Renderer -- which satisfies httpx.Renderer
//	                   because the two interfaces declare the same method.
//
// Both are reported as gaps rather than reimplemented anywhere.
package view
