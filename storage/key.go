// The tenant prefix and the key check, answered by
// github.com/arandu-io/hesape/filesystem.
//
// Path was renamed to Key on the way there, because the package now has two
// kinds of path and only one of them is a key; CleanKey kept its name. Both are
// plain functions, which have no alias form, so both are one-line calls
// through -- and the code that builds the prefix is hesape's, which is what
// makes RULE 14 one implementation rather than two that have to agree.

package storage

import (
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/filesystem"
)

// ErrNoTenant is returned when the Grant carries no tenant, or carries one that
// cannot be a path segment.
var ErrNoTenant = filesystem.ErrNoTenant

// ErrBadKey is returned for a key that would escape its tenant's prefix.
var ErrBadKey = filesystem.ErrBadKey

// Path builds the stored path for a key: <tenant>/<key>.
//
// Every driver calls it, which is what makes tenant isolation a property of the
// contract rather than of each implementation remembering. The tenant comes from
// the Grant and never from the key, so naming another tenant in the key reaches
// nothing (RULE 14).
//
// Renamed on the way to hesape: it is filesystem.Key there.
func Path(g security.Grant, key string) (string, error) { return filesystem.Key(g, key) }

// CleanKey normalizes a key and refuses one that would escape.
//
// This is the check that matters. A key is often a filename that came from an
// upload, and "../../../etc/passwd" is what an upload form eventually receives.
// Rejecting rather than sanitizing: a key that had to be rewritten to be safe is
// a key the caller did not mean, and silently storing it somewhere else is worse
// than an error.
func CleanKey(key string) (string, error) { return filesystem.CleanKey(key) }
