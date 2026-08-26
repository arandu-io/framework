// Package routes identifies modules that own framework-managed HTTP routes.
package routes

import (
	"reflect"
	"strings"
)

const frameworkImportPath = "github.com/arandu-io/framework"

// ReservedNamespace marks a first-party module as an owner of routes under the
// framework's reserved HTTP namespace.
//
// The package lives under internal so application and third-party module code
// cannot import or embed this marker.
type ReservedNamespace struct{}

func (ReservedNamespace) ownsReservedNamespace() {}

type reservedNamespaceOwner interface {
	ownsReservedNamespace()
}

// OwnsReservedNamespace reports whether v carries the private framework route
// marker and its direct dynamic type belongs to this module. Requiring both
// prevents an external type from inheriting the marker by embedding a marked
// first-party module and replacing its Routes method.
func OwnsReservedNamespace(v any) bool {
	if _, ok := v.(reservedNamespaceOwner); !ok {
		return false
	}

	t := reflect.TypeOf(v)
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil {
		return false
	}

	path := t.PkgPath()
	return path == frameworkImportPath || strings.HasPrefix(path, frameworkImportPath+"/")
}
