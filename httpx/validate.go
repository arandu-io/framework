package httpx

import (
	"github.com/arandu-io/framework/validation"
)

// Validate runs a compiled rule set over the submitted form and returns what
// passed.
//
//	in, err := ctx.Validate(requests.StorePost)
//	if err != nil {
//		return err
//	}
//
//	post, err := c.posts.Create(ctx.Ctx(), grant, posts.New{
//		Title: in.String("title"),
//		Body:  in.String("body"),
//	})
//
// Those three lines are the whole of the failure path, and there is nothing in
// them a controller can get wrong differently from every other controller: it is
// the branch every Go programmer already writes, with the only correct body.
//
// # It writes nothing
//
// Not the status, not the messages, not the redirect. The returned
// validation.Errors IS the answer, and returning it hands the decision to
// Router.handleCtx, where the flash and the redirect happen once for the whole
// application. This replaced forty-one hand-written rejection paths, of which
// several answered differently and one answered a status htmx throws away.
//
// # What comes back
//
// Only the fields the set declares are in the Input, so a value nobody wrote a
// rule for cannot reach a repository -- see validation.Input.
//
// The error is nil, explicitly, when nothing failed. A typed nil would be an
// error interface that is not nil, and `if err != nil` would be true on every
// successful request in the framework: the form would be answered with a
// redirect back to itself carrying no messages, forever. That is why this
// returns `error` and asks Any() rather than returning validation.Errors and
// letting each caller remember.
//
// # The query string is not read
//
// The form body is, and the query string is not, because a rule set describes
// what a form submits. A filter or a page number arriving in the URL is read
// with ctx.Query, and mixing the two would let a value be supplied by whichever
// half the caller did not think about -- which is the shape of a mass-assignment
// bug.
func (c *Context) Validate(s *validation.Set) (validation.Input, error) {
	if s == nil {
		// A nil set validates nothing, so every field would pass and the
		// handler would carry on with an empty Input -- silently writing zero
		// values. It is a wiring mistake and it is answered like one, by the
		// same path that answers any other failure the handler cannot handle.
		panic("httpx: ctx.Validate was given no rule set. Compile one in a package-level var: var StorePost = validation.MustCompile(validation.Rules{...})")
	}

	// ParseForm rather than the body directly: it is what fills PostForm, it is
	// idempotent, and it is the same call Reject makes when it flashes what was
	// typed. A body this could not read is an empty form, which the rules reject
	// as one -- a required field that is absent is exactly what `required` is
	// for, and reporting the parse failure instead would answer a person's typo
	// with a stack trace.
	_ = c.Request.ParseForm()

	in, errs := s.Validate(c.Request.PostForm)
	if errs.Any() {
		return in, errs
	}
	return in, nil
}
