package openapi

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// contract is a document in the shape openapi:export writes: component
// schemas referenced from the responses, a nullable object written as anyOf
// with null, a response component, a range, a default and a required header.
const contract = `{
  "openapi": "3.1.0",
  "info": {"title": "Fixture", "version": "1"},
  "paths": {
    "/users": {
      "get": {
        "operationId": "listUsers",
        "responses": {
          "200": {
            "description": "The users.",
            "headers": {"X-Total": {"required": true, "schema": {"type": "integer"}}},
            "content": {"application/json": {"schema": {
              "type": "object",
              "properties": {"data": {"type": "array", "items": {"$ref": "#/components/schemas/User"}}},
              "required": ["data"]
            }}}
          }
        }
      },
      "post": {
        "operationId": "createUser",
        "responses": {
          "2XX": {"description": "Created.", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/User"}}}},
          "default": {"description": "Refused.", "content": {"application/problem+json": {"schema": {"$ref": "#/components/schemas/Problem"}}}}
        }
      }
    },
    "/users/{id}": {
      "get": {
        "operationId": "getUser",
        "responses": {
          "200": {"description": "One user.", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/User"}}}},
          "404": {"$ref": "#/components/responses/NotFound"}
        }
      },
      "delete": {
        "responses": {"204": {"description": "Deleted."}, "303": {"description": "Sent elsewhere."}}
      }
    },
    "/merged": {
      "get": {
        "operationId": "merged",
        "responses": {"200": {"description": "Merged.", "content": {"application/json": {"schema": {
          "allOf": [{"$ref": "#/components/schemas/User"}, {"type": "object"}]
        }}}}}
      }
    }
  },
  "components": {
    "schemas": {
      "User": {
        "type": "object",
        "properties": {
          "id": {"type": "string"},
          "email": {"type": "string"},
          "manager": {"anyOf": [{"$ref": "#/components/schemas/Manager"}, {"type": "null"}]}
        },
        "required": ["id", "email"],
        "additionalProperties": false
      },
      "Manager": {
        "type": "object",
        "properties": {"name": {"type": "string"}},
        "required": ["name"]
      },
      "Problem": {"type": "object", "properties": {"title": {"type": "string"}}, "required": ["title"]}
    },
    "responses": {
      "NotFound": {"description": "Missing.", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Problem"}}}}
    }
  }
}`

func parse(t *testing.T) *Document {
	t.Helper()
	doc, err := Parse([]byte(contract))
	if err != nil {
		t.Fatalf("parsing the fixture: %v", err)
	}
	return doc
}

// reply answers every request with one status, content type, header and body.
func reply(status int, contentType, body string, headers ...string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i+1 < len(headers); i += 2 {
			w.Header().Set(headers[i], headers[i+1])
		}
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
}

// failures runs a check against a recording TB and answers what it reported.
// A Fatalf ends the check the way it ends a test: the goroutine exits.
func failures(check func(tb testing.TB)) []string {
	rec := &recordingTB{}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		check(rec)
	}()
	wg.Wait()
	return rec.messages
}

type recordingTB struct {
	testing.TB
	messages []string
}

func (*recordingTB) Helper() {}

func (r *recordingTB) Errorf(format string, args ...any) {
	r.messages = append(r.messages, fmt.Sprintf(format, args...))
}

func (r *recordingTB) Fatalf(format string, args ...any) {
	r.messages = append(r.messages, fmt.Sprintf(format, args...))
	runtime.Goexit()
}

func onlyFailure(t *testing.T, got []string, wants ...string) {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("got %d failures, want 1: %q", len(got), got)
	}
	for _, want := range wants {
		if !strings.Contains(got[0], want) {
			t.Errorf("the failure does not say %q:\n%s", want, got[0])
		}
	}
}

func TestAnAnswerTheDocumentDescribesPasses(t *testing.T) {
	doc := parse(t)
	app := reply(200, "application/json; charset=utf-8",
		`{"data":[{"id":"u1","email":"a@example.test","manager":null},{"id":"u2","email":"b@example.test","manager":{"name":"Ana"}}]}`,
		"X-Total", "2")
	got := failures(func(tb testing.TB) {
		result := doc.Check(tb, Handler(app), Call{Operation: "listUsers", Status: 200})
		if result.Status != 200 || !strings.Contains(string(result.Body), "u2") {
			tb.Errorf("the result was not handed back: %+v", result)
		}
	})
	if len(got) != 0 {
		t.Fatalf("a conforming answer failed: %q", got)
	}
}

func TestABodyThatBreaksTheSchemaNamesThePathAndTheSchemaPointer(t *testing.T) {
	doc := parse(t)
	app := reply(200, "application/json", `{"data":[{"id":"u1","email":7,"manager":{"title":"x"}}]}`, "X-Total", "1")
	got := failures(func(tb testing.TB) { doc.Check(tb, Handler(app), Call{Operation: "GET /users"}) })
	onlyFailure(t, got,
		"GET /users (listUsers) answered 200",
		"body.data[0].email must be a string (schema #/components/schemas/User/properties/email)",
		"body.data[0].manager.name is required (schema #/components/schemas/Manager/properties/name)",
		`{"data":[{"id":"u1"`,
	)
}

func TestAPropertyTheSchemaClosesIsNamedAtTheObjectThatRefusedIt(t *testing.T) {
	doc := parse(t)
	err := doc.Verify("getUser", 200, http.Header{"Content-Type": {"application/json"}}, []byte(`{"id":"u1","email":"a@example.test","password":"x"}`))
	if err == nil || !strings.Contains(err.Error(), "body.password is not a declared property (schema #/components/schemas/User)") {
		t.Fatalf("Verify answered %v", err)
	}
}

func TestAnUndocumentedStatusFails(t *testing.T) {
	doc := parse(t)
	got := failures(func(tb testing.TB) {
		doc.Check(tb, Handler(reply(500, "text/plain", "boom")), Call{Operation: "getUser", Params: map[string]string{"id": "u1"}})
	})
	onlyFailure(t, got, "GET /users/{id} (getUser) answered 500", "the status 500 is not documented; the operation documents 200, 404", "boom")
}

func TestAResponseComponentIsFollowed(t *testing.T) {
	doc := parse(t)
	if err := doc.Verify("getUser", 404, http.Header{"Content-Type": {"application/json"}}, []byte(`{"title":"Not found"}`)); err != nil {
		t.Fatalf("a documented 404 failed: %v", err)
	}
	err := doc.Verify("getUser", 404, http.Header{"Content-Type": {"application/json"}}, []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "body.title is required (schema #/components/schemas/Problem/properties/title)") {
		t.Fatalf("Verify answered %v", err)
	}
}

func TestARangeAndDefaultAreDocumentedStatuses(t *testing.T) {
	doc := parse(t)
	if err := doc.Verify("createUser", 201, http.Header{"Content-Type": {"application/json"}}, []byte(`{"id":"u1","email":"a@example.test"}`)); err != nil {
		t.Fatalf("201 under 2XX failed: %v", err)
	}
	if err := doc.Verify("createUser", 422, http.Header{"Content-Type": {"application/problem+json"}}, []byte(`{"title":"Invalid"}`)); err != nil {
		t.Fatalf("422 under default failed: %v", err)
	}
	err := doc.Verify("createUser", 422, http.Header{"Content-Type": {"application/problem+json"}}, []byte(`{"title":3}`))
	if err == nil || !strings.Contains(err.Error(), "body.title must be a string") {
		t.Fatalf("a +json body was not checked: %v", err)
	}
}

func TestAContentTypeTheResponseDoesNotDocumentFails(t *testing.T) {
	doc := parse(t)
	err := doc.Verify("getUser", 200, http.Header{"Content-Type": {"text/html; charset=utf-8"}}, []byte(`<p>hi</p>`))
	if err == nil || !strings.Contains(err.Error(), "the Content-Type is text/html; #/paths/~1users~1{id}/get/responses/200 documents application/json") {
		t.Fatalf("Verify answered %v", err)
	}
}

func TestABodyOnAResponseThatDocumentsNoneFails(t *testing.T) {
	doc := parse(t)
	if err := doc.Verify("DELETE /users/{id}", 204, http.Header{}, nil); err != nil {
		t.Fatalf("an empty 204 failed: %v", err)
	}
	err := doc.Verify("delete /users/{id}", 204, http.Header{"Content-Type": {"text/plain"}}, []byte("gone"))
	if err == nil || !strings.Contains(err.Error(), "the response has a body, and #/paths/~1users~1{id}/delete/responses/204 documents none") {
		t.Fatalf("Verify answered %v", err)
	}
}

func TestARequiredHeaderMustBeThere(t *testing.T) {
	doc := parse(t)
	err := doc.Verify("listUsers", 200, http.Header{"Content-Type": {"application/json"}}, []byte(`{"data":[]}`))
	if err == nil || !strings.Contains(err.Error(), "the header X-Total is missing") {
		t.Fatalf("Verify answered %v", err)
	}
}

func TestASchemaTheValidatorCannotRepresentIsRefusedNotPassed(t *testing.T) {
	doc := parse(t)
	err := doc.Verify("merged", 200, http.Header{"Content-Type": {"application/json"}}, []byte(`{"anything":true}`))
	if err == nil || !strings.Contains(err.Error(), "the schema uses allOf at #/paths/~1merged/get/responses/200/content/application~1json/schema, which hesape/jsonschema cannot check") {
		t.Fatalf("Verify answered %v", err)
	}
}

func TestTheStatusTheTestExpectsIsHeldToo(t *testing.T) {
	doc := parse(t)
	app := reply(404, "application/json", `{"title":"Not found"}`)
	got := failures(func(tb testing.TB) {
		doc.Check(tb, Handler(app), Call{Operation: "getUser", Params: map[string]string{"id": "u1"}, Status: 200})
	})
	onlyFailure(t, got, "the status is 404, and the test expected 200")
}

func TestTheRequestIsBuiltFromTheTemplateAndTheCall(t *testing.T) {
	doc := parse(t)
	var seen *http.Request
	var body string
	app := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r
		read, _ := io.ReadAll(r.Body)
		body = string(read)
		w.WriteHeader(http.StatusNoContent)
	})
	got := failures(func(tb testing.TB) {
		doc.Check(tb, Handler(app), Call{
			Operation: "DELETE /users/{id}",
			Params:    map[string]string{"id": "a b/c"},
			Query:     map[string][]string{"force": {"1"}},
			Header:    http.Header{"X-Trace": {"t1"}},
			Body:      map[string]string{"reason": "test"},
			Prepare:   func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "session", Value: "s1"}) },
			Status:    http.StatusNoContent,
		})
	})
	if len(got) != 0 {
		t.Fatalf("the call failed: %q", got)
	}
	if seen.Method != http.MethodDelete || seen.URL.EscapedPath() != "/users/a%20b%2Fc" || seen.URL.Query().Get("force") != "1" {
		t.Fatalf("the request went to %s %s", seen.Method, seen.URL.RequestURI())
	}
	if seen.Header.Get("X-Trace") != "t1" || seen.Header.Get("Content-Type") != "application/json" || body != `{"reason":"test"}` {
		t.Fatalf("the request carried %v and %q", seen.Header, body)
	}
	if cookie, err := seen.Cookie("session"); err != nil || cookie.Value != "s1" {
		t.Fatalf("Prepare did not run: %v", err)
	}
}

func TestAParameterTheTemplateDoesNotNameIsRefused(t *testing.T) {
	doc := parse(t)
	got := failures(func(tb testing.TB) {
		doc.Check(tb, Handler(reply(200, "", "")), Call{Operation: "getUser", Params: map[string]string{"id": "1", "slug": "x"}})
	})
	onlyFailure(t, got, "Params names slug, which the path template \"/users/{id}\" does not have")

	got = failures(func(tb testing.TB) { doc.Check(tb, Handler(reply(200, "", "")), Call{Operation: "getUser"}) })
	onlyFailure(t, got, "the path needs the parameter \"id\", and Params has none")
}

func TestAnUnknownOperationIsNamedInTheFailure(t *testing.T) {
	doc := parse(t)
	got := failures(func(tb testing.TB) { doc.Check(tb, Handler(reply(200, "", "")), Call{Operation: "GET /nowhere"}) })
	onlyFailure(t, got, `the document has no operation "GET /nowhere"`)
	if ops := doc.Operations(); strings.Join(ops, ",") != "GET /merged,GET /users,POST /users,GET /users/{id},DELETE /users/{id}" {
		t.Fatalf("Operations() = %v", ops)
	}
}

func TestARunningServerIsCheckedWithoutFollowingItsRedirect(t *testing.T) {
	doc := parse(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users/u1" {
			w.WriteHeader(http.StatusTeapot)
			return
		}
		http.Redirect(w, r, "/elsewhere", http.StatusSeeOther)
	}))
	defer server.Close()
	got := failures(func(tb testing.TB) {
		// http.Redirect writes a short body for a GET only, so a DELETE
		// answers 303 with nothing in it, which is what the document says.
		doc.Check(tb, URL(server.URL+"/", nil), Call{Operation: "DELETE /users/{id}", Params: map[string]string{"id": "u1"}, Status: http.StatusSeeOther})
	})
	if len(got) != 0 {
		t.Fatalf("the redirect was not checked as it was answered: %q", got)
	}
}

func TestADocumentThatIsNotOpenAPIIsRefused(t *testing.T) {
	if _, err := Parse([]byte(`{"swagger":"2.0"}`)); err == nil || !strings.Contains(err.Error(), "want 3.1") {
		t.Fatalf("Parse answered %v", err)
	}
	if _, err := Parse([]byte(`{"openapi":"3.1.0","paths":{"/a":{"get":{"operationId":"x"}},"/b":{"get":{"operationId":"x"}}}}`)); err == nil || !strings.Contains(err.Error(), `operationId "x" names two operations`) {
		t.Fatalf("Parse answered %v", err)
	}
}

func TestPointerForWalksReferencesNullableBranchesAndLists(t *testing.T) {
	doc := parse(t)
	base := "#/paths/~1users/get/responses/200/content/application~1json/schema"
	for path, want := range map[string]string{
		"":                       base,
		"data":                   base + "/properties/data",
		"data[3]":                "#/components/schemas/User",
		"data[3].manager.name":   "#/components/schemas/Manager/properties/name",
		"data[3].unknown":        "#/components/schemas/User",
		"data[3].manager[0].odd": "#/components/schemas/Manager",
	} {
		if got := doc.pointerFor(base, path); got != want {
			t.Errorf("pointerFor(%q) = %s, want %s", path, got, want)
		}
	}
}
