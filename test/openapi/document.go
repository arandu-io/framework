package openapi

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/arandu-io/hesape/jsonschema"
)

// methods are the operation keys of a Path Item Object, in the order the
// specification lists them.
var methods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

// Document is a parsed OpenAPI 3.1 document and the operations in it.
//
// It is safe for concurrent use: the schemas it reads are built once per
// pointer and kept.
type Document struct {
	raw        map[string]any
	operations map[string]*operation
	names      []string

	mu      sync.Mutex
	schemas map[string]compiled
}

// operation is one method on one path, with the pointer it lives at.
type operation struct {
	method  string
	path    string
	id      string
	pointer string
	raw     map[string]any
}

// label is how a failure names the operation: its method and path, and its
// operationId when it has one.
func (o *operation) label() string {
	name := strings.ToUpper(o.method) + " " + o.path
	if o.id != "" {
		name += " (" + o.id + ")"
	}
	return name
}

// compiled is one schema read for validation, or the reason it could not be.
type compiled struct {
	schema jsonschema.Type
	err    error
}

// Load reads the document at path, failing the test when it cannot be read or
// is not an OpenAPI document.
func Load(t testing.TB, path string) *Document {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("openapi: reading the document: %v", err)
	}
	doc, err := Parse(data)
	if err != nil {
		t.Fatalf("openapi: %s: %v", path, err)
	}
	return doc
}

// Parse reads a document from its JSON form, as openapi:export writes it.
func Parse(data []byte) (*Document, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("the document is not JSON: %w", err)
	}
	version, _ := raw["openapi"].(string)
	if !strings.HasPrefix(version, "3.") {
		return nil, fmt.Errorf("the document declares openapi %q, want 3.1", version)
	}
	doc := &Document{raw: raw, operations: map[string]*operation{}, schemas: map[string]compiled{}}
	paths, _ := raw["paths"].(map[string]any)
	templates := make([]string, 0, len(paths))
	for template := range paths {
		templates = append(templates, template)
	}
	sort.Strings(templates)
	for _, template := range templates {
		pointer := "#/paths/" + escape(template)
		item, itemPointer, err := doc.resolve(paths[template], pointer)
		if err != nil {
			return nil, err
		}
		for _, method := range methods {
			raw, ok := item[method].(map[string]any)
			if !ok {
				continue
			}
			op := &operation{method: method, path: template, pointer: itemPointer + "/" + method, raw: raw}
			op.id, _ = raw["operationId"].(string)
			key := strings.ToUpper(method) + " " + template
			doc.operations[key] = op
			doc.names = append(doc.names, key)
			if op.id != "" {
				if other, taken := doc.operations[op.id]; taken && other != op {
					return nil, fmt.Errorf("operationId %q names two operations", op.id)
				}
				doc.operations[op.id] = op
			}
		}
	}
	return doc, nil
}

// Operations lists every operation in the document by method and path, in
// path order.
func (d *Document) Operations() []string {
	return append([]string(nil), d.names...)
}

// find looks an operation up by operationId or by "METHOD /path".
func (d *Document) find(name string) (*operation, error) {
	if op, ok := d.operations[name]; ok {
		return op, nil
	}
	if method, path, ok := strings.Cut(name, " "); ok {
		if op, ok := d.operations[strings.ToUpper(method)+" "+path]; ok {
			return op, nil
		}
	}
	return nil, fmt.Errorf("the document has no operation %q; name one by operationId or as \"GET /path/{param}\"", name)
}

// resolve follows a $ref to the object it points at, and answers the pointer
// the object lives at.
func (d *Document) resolve(value any, pointer string) (map[string]any, string, error) {
	for range 32 {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, pointer, fmt.Errorf("%s is not an object", pointer)
		}
		ref, ok := object["$ref"].(string)
		if !ok {
			return object, pointer, nil
		}
		target, err := d.lookup(ref)
		if err != nil {
			return nil, pointer, err
		}
		value, pointer = target, ref
	}
	return nil, pointer, fmt.Errorf("%s is a chain of more than 32 references", pointer)
}

// lookup reads a local JSON pointer out of the document.
func (d *Document) lookup(ref string) (any, error) {
	if !strings.HasPrefix(ref, "#/") {
		return nil, fmt.Errorf("the reference %q is not local to the document", ref)
	}
	var target any = d.raw
	for _, segment := range strings.Split(ref[2:], "/") {
		object, ok := target.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("the reference %q does not resolve", ref)
		}
		target, ok = object[unescape(segment)]
		if !ok {
			return nil, fmt.Errorf("the reference %q does not resolve", ref)
		}
	}
	return target, nil
}

// schemaAt builds the validator for the schema at pointer once, and keeps it.
func (d *Document) schemaAt(pointer string) (jsonschema.Type, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if c, ok := d.schemas[pointer]; ok {
		return c.schema, c.err
	}
	var c compiled
	if keyword, where := d.unsupported(pointer); keyword != "" {
		c.err = fmt.Errorf("the schema uses %s at %s, which hesape/jsonschema cannot check", keyword, where)
	} else {
		// The fragment is read with the document's paths and components beside
		// it, so its "#/components/schemas/..." references resolve where the
		// document wrote them. The two keys are not schema keywords, and the
		// deserializer reads none of them.
		root := map[string]any{"$ref": pointer, "paths": d.raw["paths"], "components": d.raw["components"]}
		c.schema, c.err = jsonschema.Deserialize(root)
	}
	d.schemas[pointer] = c
	return c.schema, c.err
}

// escape encodes one JSON pointer segment.
func escape(segment string) string {
	return strings.ReplaceAll(strings.ReplaceAll(segment, "~", "~0"), "/", "~1")
}

// unescape decodes one JSON pointer segment.
func unescape(segment string) string {
	return strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~")
}
