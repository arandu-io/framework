// Package http is the request and routing layer.
//
// It is a thin shell over net/http: Middleware is the standard
// func(http.Handler) http.Handler, so every middleware written for the Go
// ecosystem works here unchanged.
//
// This package is a bridge. It is removed in v1.0.0; import github.com/arandu-io/hesape/http directly.
//
// The components moved to github.com/arandu-io/hesape, under new names, and
// this package is now the old names pointing at them. One package here answers
// to four there, and which one a symbol went to depends on the symbol:
//
//	hesape/http                 Context, Renderer, State, Redirect, Refuse, Reject, Back
//	hesape/routing              Route
//	hesape/routing/controllers  Router, NewRouter, Routes, the resource controller interfaces, Invoker
//	hesape/pipeline             Middleware and Chain, generified over the handler type
//
// Nothing here holds an implementation: every type is a Go alias, so the old
// name and the new one are one type to the compiler, and every function is a
// forward.
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
// Get..Delete add. The adapter that does it is hesape/http.NewAdapter, built
// from the router's renderer, route table and flash.
package http
