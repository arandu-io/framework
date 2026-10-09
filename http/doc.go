// Package http is the request and routing layer.
//
// It is a thin shell over net/http: Middleware is the standard
// func(http.Handler) http.Handler, so every middleware written for the Go
// ecosystem works here unchanged.
//
// This package is a bridge. It is removed in v1.0.0; import github.com/arandu-io/hesape/http directly.
//
// The components moved to github.com/arandu-io/hesape, under new names, and
// this package is now the old names pointing at them. It is the widest split in
// the collection: one package here answers to three there, and which one a
// symbol went to depends on the symbol.
//
//	hesape/http      Context, Renderer, State, Redirect, Refuse, Reject, Back
//	hesape/routing   Router, Route, Routes, the resource controller interfaces, Invoker
//	hesape/pipeline  Middleware and Chain, generified over the handler type
//
// The death date above is what keeps this from being a second way to import one
// type. Nothing here holds an implementation: where the name and the signature
// survived the move it is a Go alias, and where the design diverged it is an
// envelope that translates and nothing more.
//
// The three envelopes, and what diverged:
//
//	Router   hesape/routing.Router holds no request state, takes a Group struct
//	         rather than a prefix and a variadic, and has neither Action nor a
//	         method-shaped Resource, Singleton, ResourceAction or Invokable
//	Routes   Routes.URL was renamed Routes.Route, and a method cannot be
//	         declared on another package's type
//	Resource the seven action interfaces gained a type parameter, so each one
//	         is an alias to an INSTANTIATED generic rather than to a plain type
//
// One symbol was deleted rather than bridged: Context.Validate. It had no
// caller outside its own test and it was the second way to validate a request.
//
// # One registration path
//
// Get, Post, Put, Patch, Delete, Action, Resource, Singleton, ResourceAction
// and Invokable are ten method names over one registration. They are not ten
// features, and the difference between them is not how a route is matched --
// it is what shape of handler the caller is holding:
//
//	Get..Delete     an http.HandlerFunc, registered as it stands
//	Action          a func(*Context) error, made into a handler first
//	Resource        the same, for the actions one controller value implements
//	Singleton       the same, for show, edit and update of a resource with no id
//	ResourceAction  one action, under the record path Resource gives the name
//	Invokable       a controller whose one action is Invoke
//
// Group registers nothing; it scopes what is registered after it.
//
// Turning a controller action into a handler is the only thing the five after
// Get..Delete add, and it is the request layer's job: it needs the renderer,
// the route table and the flash, which are the three things
// github.com/arandu-io/hesape/routing holds none of. So that package offers
// one handler type and takes the adaptation as a parameter, and the adapter
// lives here.
//
// It lives here unexported, and that is the whole of what is unfinished.
// Get..Delete and Group each have an exact replacement line in hesape/routing
// and are ready to migrate today; the five that take a controller action do
// not, because the adapter they need has no exported name on either side of
// the boundary. hesape/routing declares no adapter and names none: it takes one
// as a parameter, routing.Adapter, and leaves writing it to the layer that owns
// the request context. So the line to migrate them to is the one that exports
// this adapter, and it does not exist yet. Until it does, these five are how a
// controller reaches a route, and this package outliving them is not the plan.
package http
