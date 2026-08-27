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
//	hesape/routing   Router, Route, Routes, the resource controller interfaces
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
//	         method-shaped Resource
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
// Get, Post, Put, Patch, Delete, Action and Resource are seven method names
// over one registration. They are not seven features, and the difference
// between them is not how a route is matched -- it is what shape of handler
// the caller is holding:
//
//	Get..Delete  an http.HandlerFunc, registered as it stands
//	Action       a func(*Context) error, made into a handler first
//	Resource     the same, for the actions one controller value implements
//
// Group registers nothing; it scopes what is registered after it.
//
// Turning a controller action into a handler is the only thing Action and
// Resource add, and it is the request layer's job: it needs the renderer, the
// route table and the flash, which are the three things
// github.com/arandu-io/hesape/routing holds none of. So that package offers
// one handler type and takes the adaptation as a parameter, and the adapter
// lives here.
//
// It lives here unexported, and that is the whole of what is unfinished.
// Get..Delete and Group each have an exact replacement line in hesape/routing
// and are ready to migrate today; Action and Resource do not, because the
// adapter they need has no exported name on either side of the boundary. The
// doc comments in hesape/routing already write that name as hhttp.Action and
// no such function exists -- and it cannot be written in the shape they show,
// a unary function, because a unary function reaches none of the three things
// the adaptation requires. Until it exists, Action and Resource are how a
// controller reaches a route, and this package outliving them is not the plan.
package http
