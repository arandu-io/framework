// The per-request framework state, answered by github.com/arandu-io/hesape/http.
//
// The context key is hesape's and unexported there, which is the point: the
// middleware that writes the state and the view layer that reads it back agree
// on one key across both module boundaries, so a page drawn through this
// package sees what a page drawn through hesape/http sees.

package http

import (
	"context"

	hhttp "github.com/arandu-io/hesape/http"
)

// State is what the framework knows about a request before the handler runs.
//
// Today it is one thing: what the request that redirected here failed on. It is
// a struct rather than that one thing so the next piece of per-request
// framework state -- there will be one -- does not add a second context key, a
// second middleware and a second accessor that every page has to be taught
// about.
//
// It is put on the request context by middleware.Flash and read by view.New.
// Nothing else writes it, and no handler ever has to.
//
// Its Errors field is hesape/validation.Errors, which has the same underlying
// map as the framework's. A framework validation.Errors converts into it and a
// plain map[string][]string assigns straight to it.
type State = hhttp.State

// WithState returns a context carrying the framework's per-request state.
//
// middleware.Flash calls it. It is exported for the test that drives a request
// past the middleware, and for nothing else.
func WithState(parent context.Context, s State) context.Context {
	return hhttp.WithState(parent, s)
}

// StateFrom returns the state on a request context, or the zero State.
//
// The zero value is the answer for every request that was not redirected here
// from a rejection, which is nearly all of them: no errors and no old input is
// a page that draws neither, not a page that has to check first.
func StateFrom(ctx context.Context) State { return hhttp.StateFrom(ctx) }
