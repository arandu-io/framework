package openapi

import (
	"sort"
	"strconv"
	"strings"
)

// uncheckable are the JSON Schema keywords that constrain a value and that
// hesape/jsonschema does not represent. The deserializer reads past them, so a
// schema carrying one would be checked as something weaker than it says.
var uncheckable = []string{
	"allOf", "not", "if", "then", "else", "const",
	"prefixItems", "contains", "minContains", "maxContains", "additionalItems",
	"patternProperties", "propertyNames", "dependentSchemas", "dependentRequired",
	"unevaluatedProperties", "unevaluatedItems",
	"minProperties", "maxProperties", "exclusiveMinimum", "exclusiveMaximum",
}

// unsupported walks the schema at pointer, following its references, and
// answers the first keyword it cannot check and where it is. Both are empty
// when the whole schema is checkable.
func (d *Document) unsupported(pointer string) (keyword, where string) {
	seen := map[string]bool{}
	var walk func(value any, at string) (string, string)
	walk = func(value any, at string) (string, string) {
		object, ok := value.(map[string]any)
		if !ok {
			return "", ""
		}
		if ref, ok := object["$ref"].(string); ok {
			if !seen[ref] {
				seen[ref] = true
				if target, err := d.lookup(ref); err == nil {
					if k, w := walk(target, ref); k != "" {
						return k, w
					}
				}
			}
		}
		for _, k := range uncheckable {
			if _, present := object[k]; present {
				return k, at
			}
		}
		if properties, ok := object["properties"].(map[string]any); ok {
			names := make([]string, 0, len(properties))
			for name := range properties {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				if k, w := walk(properties[name], at+"/properties/"+escape(name)); k != "" {
					return k, w
				}
			}
		}
		for _, key := range []string{"items", "additionalProperties"} {
			if k, w := walk(object[key], at+"/"+key); k != "" {
				return k, w
			}
		}
		for _, key := range []string{"anyOf", "oneOf"} {
			branches, _ := object[key].([]any)
			for i, branch := range branches {
				if k, w := walk(branch, at+"/"+key+"/"+strconv.Itoa(i)); k != "" {
					return k, w
				}
			}
		}
		return "", ""
	}
	return walk(map[string]any{"$ref": pointer}, pointer)
}

// pointerFor answers the JSON pointer of the schema that governs the value at
// path inside a body checked against the schema at base.
//
// path is the dotted form hesape/jsonschema reports a problem at: "data",
// "data[2].email". The walk follows references to the component they name, and
// into the one non-null branch of a nullable anyOf or oneOf, which is the shape
// a generator writes for an optional value. Where a segment names nothing the
// schema declares -- an undeclared property -- it stops at the deepest schema
// it reached, which is the one that refused the value.
func (d *Document) pointerFor(base, path string) string {
	current := base
	value, err := d.lookup(base)
	if err != nil {
		return base
	}
	settle := func() {
		for range 32 {
			object, ok := value.(map[string]any)
			if !ok {
				return
			}
			if ref, ok := object["$ref"].(string); ok {
				target, err := d.lookup(ref)
				if err != nil {
					return
				}
				value, current = target, ref
				continue
			}
			branch, at := nonNullBranch(object)
			if branch == nil {
				return
			}
			value, current = branch, current+at
		}
	}
	settle()
	for _, segment := range splitPath(path) {
		object, ok := value.(map[string]any)
		if !ok {
			break
		}
		var next any
		var at string
		if segment.index {
			next, at = object["items"], "/items"
		} else if properties, ok := object["properties"].(map[string]any); ok {
			next, at = properties[segment.name], "/properties/"+escape(segment.name)
		}
		if next == nil {
			break
		}
		value, current = next, current+at
		settle()
	}
	return current
}

// nonNullBranch is the single branch of a nullable anyOf or oneOf that is not
// the null one, with the pointer suffix that reaches it. It is nil for every
// other shape.
func nonNullBranch(object map[string]any) (map[string]any, string) {
	for _, key := range []string{"anyOf", "oneOf"} {
		branches, ok := object[key].([]any)
		if !ok {
			continue
		}
		var found map[string]any
		var at string
		for i, branch := range branches {
			b, ok := branch.(map[string]any)
			if !ok {
				return nil, ""
			}
			if b["type"] == "null" {
				continue
			}
			if found != nil {
				return nil, ""
			}
			found, at = b, "/"+key+"/"+strconv.Itoa(i)
		}
		return found, at
	}
	return nil, ""
}

// pathSegment is one step of a problem path: a property name or a list index.
type pathSegment struct {
	name  string
	index bool
}

// splitPath reads the dotted form hesape/jsonschema reports: "a.b[2].c".
func splitPath(path string) []pathSegment {
	var out []pathSegment
	for _, part := range strings.Split(path, ".") {
		if part == "" {
			continue
		}
		name, rest, _ := strings.Cut(part, "[")
		if name != "" {
			out = append(out, pathSegment{name: name})
		}
		for rest != "" {
			_, after, ok := strings.Cut(rest, "]")
			out = append(out, pathSegment{index: true})
			if !ok {
				break
			}
			rest = strings.TrimPrefix(after, "[")
		}
	}
	return out
}
