// The single-action controller, answered by
// github.com/arandu-io/hesape/routing/controllers.

package http

import "github.com/arandu-io/hesape/routing/controllers"

// Invoker is a controller with one action. It answers one route, so the action
// needs no name beyond Invoke. Router.Invokable registers one.
//
// A controller declaring Invoke(*http.Context) error satisfies it literally,
// because http.Context is hhttp.Context.
type Invoker = controllers.Invoker
