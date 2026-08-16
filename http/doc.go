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
package http
