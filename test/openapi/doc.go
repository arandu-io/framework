// Package openapi checks what an application answers against the OpenAPI
// document it publishes.
//
// The document is the one the application exports and commits, and the
// answers are real ones: a request goes through the application's own
// handler, or to a running server, and what comes back is held to the
// operation the document says was called:
//
//	contract := openapi.Load(t, "../../docs/api.openapi.json")
//	contract.Check(t, openapi.Handler(app), openapi.Call{
//		Operation: "GET /users/{id}",
//		Params:    map[string]string{"id": user.ID},
//		Status:    http.StatusOK,
//	})
//
// An operation is named by its operationId, or by its method and path template
// as the document writes them.
//
// # What is checked
//
// The status must be one the operation documents, by its exact code, by its
// range (2XX) or by default. The Content-Type must be one of the media types
// that response documents; a response that documents none must have no body.
// A JSON body must match the media type's schema, through the same
// hesape/jsonschema the application builds its schemas with. Every response
// header the document marks required must be present.
//
// A failure names the operation, the status, each problem with the path into
// the body where it is, and the JSON pointer of the schema it broke -- the
// component and property a reader opens the document at.
//
// # What is refused rather than passed
//
// A schema that uses a keyword hesape/jsonschema cannot check -- allOf, not,
// const, if/then/else, patternProperties and the rest of what it does not
// represent -- is reported as a problem. Checked against the subset, such a
// schema would pass values the document forbids, and a contract test that
// passes what it cannot check proves the opposite of what it says.
//
// # What it does not do
//
// It checks responses, not requests: the request is what the test wrote, and
// what the handler refuses is the handler's to prove. It does not generate
// calls from the document either, because the data a call needs -- a user that
// exists, a tenant the session belongs to -- is the test's to arrange.
package openapi
