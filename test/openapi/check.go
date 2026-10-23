package openapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/arandu-io/hesape/jsonschema"
)

// Target is where a call is sent: an application's handler, in this process,
// or a running server.
type Target interface {
	do(r *http.Request) (*http.Response, error)
}

// Handler sends every call through h, in this process.
func Handler(h http.Handler) Target { return handlerTarget{h: h} }

// URL sends every call to the server at base, such as "http://127.0.0.1:8080".
//
// The client does not follow redirects: a 303 is an answer the document
// describes, and following it would check the page it pointed at instead. A
// nil client uses one with that behaviour and nothing else.
//
// That client is outside the outbound guard: it reaches only the address the
// test passed, the application under test, which a test starts on loopback;
// there is no address taken from a request or a user to guard against.
func URL(base string, client *http.Client) Target {
	if client == nil {
		client = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	return urlTarget{base: strings.TrimRight(base, "/"), client: client}
}

type handlerTarget struct{ h http.Handler }

func (t handlerTarget) do(r *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	t.h.ServeHTTP(recorder, r)
	return recorder.Result(), nil
}

type urlTarget struct {
	base   string
	client *http.Client
}

func (t urlTarget) do(r *http.Request) (*http.Response, error) {
	target, err := url.Parse(t.base + r.URL.RequestURI())
	if err != nil {
		return nil, err
	}
	r.URL = target
	r.Host = target.Host
	r.RequestURI = ""
	return t.client.Do(r)
}

// Call is one request to a documented operation.
type Call struct {
	// Operation names the operation, by operationId or as "GET /users/{id}".
	Operation string
	// Params fills the path template, one value per {name}. A name the
	// template does not have, or one it has and Params lacks, is an error:
	// both are a call to some other address than the one documented.
	Params map[string]string
	// Query is added to the address.
	Query url.Values
	// Header is sent as given.
	Header http.Header
	// Body is the request body. A []byte or a string is sent as it is; any
	// other value is encoded as JSON, with Content-Type application/json
	// unless Header sets one.
	Body any
	// Prepare, when set, changes the request last, which is where a test adds
	// the cookie of a session it signed in with.
	Prepare func(*http.Request)
	// Status is the status the test expects. Zero accepts any status the
	// operation documents.
	Status int
}

// Result is what came back from a call.
type Result struct {
	Status int
	Header http.Header
	Body   []byte
}

// JSON decodes the body into v, failing the test when it cannot.
func (r Result) JSON(t testing.TB, v any) {
	t.Helper()
	if err := json.Unmarshal(r.Body, v); err != nil {
		t.Fatalf("openapi: decoding the body: %v\n%s", err, excerpt(r.Body))
	}
}

// Check sends the call, holds the answer to the document, and fails the test
// with every problem it found. The answer is returned for the test to assert
// what only it knows about.
func (d *Document) Check(t testing.TB, target Target, call Call) Result {
	t.Helper()
	op, err := d.find(call.Operation)
	if err != nil {
		t.Fatalf("openapi: %v", err)
	}
	request, err := d.request(op, call)
	if err != nil {
		t.Fatalf("openapi: %s: %v", op.label(), err)
	}
	response, err := target.do(request)
	if err != nil {
		t.Fatalf("openapi: %s: sending the request: %v", op.label(), err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatalf("openapi: %s: reading the response: %v", op.label(), err)
	}
	result := Result{Status: response.StatusCode, Header: response.Header, Body: body}

	var problems []string
	if call.Status != 0 && call.Status != result.Status {
		problems = append(problems, fmt.Sprintf("the status is %d, and the test expected %d", result.Status, call.Status))
	}
	if err := d.Verify(call.Operation, result.Status, result.Header, result.Body); err != nil {
		var mismatch *Mismatch
		if !errors.As(err, &mismatch) {
			t.Fatalf("openapi: %v", err)
		}
		problems = append(problems, mismatch.Problems...)
	}
	if len(problems) > 0 {
		t.Errorf("%s", (&Mismatch{Operation: op.label(), Status: result.Status, Problems: problems}).Error()+"\n"+excerpt(body))
	}
	return result
}

// Mismatch is an answer that does not match the document: the operation, the
// status it answered, and every problem found with it.
type Mismatch struct {
	Operation string
	Status    int
	Problems  []string
}

// Error lists every problem on its own line.
func (m *Mismatch) Error() string {
	return fmt.Sprintf("openapi: %s answered %d, which does not match the document:\n  - %s",
		m.Operation, m.Status, strings.Join(m.Problems, "\n  - "))
}

// Verify holds one answer to the operation the document describes, without a
// test to fail: it returns a *Mismatch naming every problem, or nil. Check is
// Verify with the request sent and the test failed for it.
func (d *Document) Verify(operation string, status int, header http.Header, body []byte) error {
	op, err := d.find(operation)
	if err != nil {
		return err
	}
	problems := d.verify(op, status, header, body)
	if len(problems) == 0 {
		return nil
	}
	return &Mismatch{Operation: op.label(), Status: status, Problems: problems}
}

func (d *Document) verify(op *operation, status int, header http.Header, body []byte) []string {
	responses, _ := op.raw["responses"].(map[string]any)
	key, documented := matchStatus(responses, status)
	if key == "" {
		return []string{fmt.Sprintf("the status %d is not documented; the operation documents %s", status, strings.Join(documented, ", "))}
	}
	response, pointer, err := d.resolve(responses[key], op.pointer+"/responses/"+escape(key))
	if err != nil {
		return []string{err.Error()}
	}

	var problems []string
	problems = append(problems, d.verifyHeaders(response, pointer, header)...)

	content, _ := response["content"].(map[string]any)
	if len(content) == 0 {
		if len(body) > 0 {
			problems = append(problems, fmt.Sprintf("the response has a body, and %s documents none", pointer))
		}
		return problems
	}
	declared := header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(declared)
	if err != nil {
		return append(problems, fmt.Sprintf("the Content-Type %q is not a media type; %s documents %s", declared, pointer, keys(content)))
	}
	matched := matchMediaType(content, mediaType)
	if matched == "" {
		return append(problems, fmt.Sprintf("the Content-Type is %s; %s documents %s", mediaType, pointer, keys(content)))
	}
	media, _ := content[matched].(map[string]any)
	if _, ok := media["schema"]; !ok || !isJSON(mediaType) {
		return problems
	}
	schemaPointer := pointer + "/content/" + escape(matched) + "/schema"
	schema, err := d.schemaAt(schemaPointer)
	if err != nil {
		return append(problems, err.Error()+"; the body was not checked against it")
	}
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return append(problems, fmt.Sprintf("the body is not JSON: %v", err))
	}
	if err := jsonschema.Validate(schema, value); err != nil {
		var invalid *jsonschema.Invalid
		if !errors.As(err, &invalid) {
			return append(problems, err.Error())
		}
		for _, p := range invalid.Problems {
			problems = append(problems, fmt.Sprintf("%s %s (schema %s)", bodyPath(p.Path), p.Message, d.pointerFor(schemaPointer, p.Path)))
		}
	}
	return problems
}

// verifyHeaders reports every header the response marks required and the
// answer does not carry.
func (d *Document) verifyHeaders(response map[string]any, pointer string, header http.Header) []string {
	headers, _ := response["headers"].(map[string]any)
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	var problems []string
	for _, name := range names {
		definition, _, err := d.resolve(headers[name], pointer+"/headers/"+escape(name))
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		if required, _ := definition["required"].(bool); required && header.Get(name) == "" {
			problems = append(problems, fmt.Sprintf("the header %s is missing; %s/headers/%s marks it required", name, pointer, escape(name)))
		}
	}
	return problems
}

// request builds the HTTP request a call describes.
func (d *Document) request(op *operation, call Call) (*http.Request, error) {
	path, err := fill(op.path, call.Params)
	if err != nil {
		return nil, err
	}
	if len(call.Query) > 0 {
		path += "?" + call.Query.Encode()
	}
	var body io.Reader
	contentType := ""
	switch value := call.Body.(type) {
	case nil:
	case []byte:
		body = bytes.NewReader(value)
	case string:
		body = strings.NewReader(value)
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("encoding the body: %w", err)
		}
		body, contentType = bytes.NewReader(encoded), "application/json"
	}
	request := httptest.NewRequest(strings.ToUpper(op.method), path, body)
	for name, values := range call.Header {
		for _, v := range values {
			request.Header.Add(name, v)
		}
	}
	if contentType != "" && request.Header.Get("Content-Type") == "" {
		request.Header.Set("Content-Type", contentType)
	}
	if call.Prepare != nil {
		call.Prepare(request)
	}
	return request, nil
}

// fill puts the parameters into the path template.
func fill(template string, params map[string]string) (string, error) {
	used := map[string]bool{}
	var out strings.Builder
	rest := template
	for {
		start := strings.IndexByte(rest, '{')
		if start < 0 {
			out.WriteString(rest)
			break
		}
		end := strings.IndexByte(rest[start:], '}')
		if end < 0 {
			return "", fmt.Errorf("the path template %q has an unclosed parameter", template)
		}
		name := rest[start+1 : start+end]
		value, ok := params[name]
		if !ok {
			return "", fmt.Errorf("the path needs the parameter %q, and Params has none", name)
		}
		used[name] = true
		out.WriteString(rest[:start])
		out.WriteString(url.PathEscape(value))
		rest = rest[start+end+1:]
	}
	var extra []string
	for name := range params {
		if !used[name] {
			extra = append(extra, name)
		}
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		return "", fmt.Errorf("Params names %s, which the path template %q does not have", strings.Join(extra, ", "), template)
	}
	return out.String(), nil
}

// matchStatus picks the response that documents status: the exact code, then
// its range, then default. It also answers what is documented, for the
// message when nothing matches.
func matchStatus(responses map[string]any, status int) (string, []string) {
	code := strconv.Itoa(status)
	if _, ok := responses[code]; ok {
		return code, nil
	}
	for key := range responses {
		if len(key) == 3 && strings.EqualFold(key[1:], "XX") && key[0] == code[0] {
			return key, nil
		}
	}
	if _, ok := responses["default"]; ok {
		return "default", nil
	}
	return "", sortedKeys(responses)
}

// matchMediaType picks the documented media type a response's type falls
// under: the exact one, then its type with a wildcard subtype, then */*.
func matchMediaType(content map[string]any, mediaType string) string {
	for key := range content {
		if documented, _, err := mime.ParseMediaType(key); err == nil && strings.EqualFold(documented, mediaType) {
			return key
		}
	}
	major, _, _ := strings.Cut(mediaType, "/")
	for key := range content {
		if strings.EqualFold(key, major+"/*") {
			return key
		}
	}
	if _, ok := content["*/*"]; ok {
		return "*/*"
	}
	return ""
}

// isJSON reports whether a media type carries JSON: application/json or a
// +json suffix.
func isJSON(mediaType string) bool {
	mediaType = strings.ToLower(mediaType)
	return mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
}

// bodyPath renders a problem path as a place in the body.
func bodyPath(path string) string {
	switch {
	case path == "":
		return "the body"
	case strings.HasPrefix(path, "["):
		return "body" + path
	}
	return "body." + path
}

func keys(content map[string]any) string { return strings.Join(sortedKeys(content), ", ") }

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// excerpt is the start of a body, for a failure message.
func excerpt(body []byte) string {
	const limit = 512
	if len(body) == 0 {
		return "(empty body)"
	}
	if len(body) > limit {
		return string(body[:limit]) + "..."
	}
	return string(body)
}
