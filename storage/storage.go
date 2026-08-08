// Package storage is the file contract: what a tenant uploaded, and where it
// went.
//
// The contract lives in the core and the drivers do not, for the same reason as
// data.Repository and jobs.Queue: every operation takes a security.Grant, and
// the path is prefixed by the tenant that Grant carries. A file is customer
// data, and a path without a tenant is a leak with a directory name.
//
// There is no symlink into a document root. Publishing a storage directory that
// way makes every stored file world-readable by URL and turns authorization into
// "hope nobody guesses the name". Here a file is served by a route, and the
// route runs a Policy like any other.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"
)

// ErrNotFound is returned when a key does not exist for this tenant.
//
// It is the same error whether the file is absent or belongs to somebody else,
// and that is deliberate: distinguishing them would tell a caller which keys
// exist in other tenants.
var ErrNotFound = errors.New("storage: not found")

// ErrNoTenant is returned when the Grant carries no tenant.
var ErrNoTenant = errors.New("storage: the Grant carries no tenant, and a file without one belongs to everybody")

// ErrBadKey is returned for a key that would escape its tenant's prefix.
var ErrBadKey = errors.New("storage: the key escapes the tenant prefix")

// File is what a Get returns.
type File struct {
	Key         string
	Size        int64
	ContentType string
	ModifiedAt  time.Time
	// Body is the content. The caller closes it.
	Body io.ReadCloser
}

// Store is what a driver implements.
//
// Every method takes a Grant. That is not ceremony: it is the only thing
// standing between "the application stores files" and "any handler can read any
// customer's files by building a string".
type Store interface {
	// Put writes a file under the tenant of the Grant.
	Put(ctx context.Context, g security.Grant, key string, body io.Reader, contentType string) error
	// Get reads one back.
	Get(ctx context.Context, g security.Grant, key string) (File, error)
	// Delete removes it. Removing what is not there is not an error.
	Delete(ctx context.Context, g security.Grant, key string) error
	// List returns the keys under a prefix, without the tenant part.
	List(ctx context.Context, g security.Grant, prefix string) ([]string, error)
	// Exists reports whether the key is there.
	Exists(ctx context.Context, g security.Grant, key string) (bool, error)
}

// Path builds the stored path for a key: <tenant>/<key>.
//
// Every driver calls it, which is what makes tenant isolation a property of the
// contract rather than of each implementation remembering.
func Path(g security.Grant, key string) (string, error) {
	tenant := data.Tenant(g)
	if tenant == "" {
		return "", ErrNoTenant
	}
	// The key is checked below, and for a long time the tenant was not -- so
	// tenant "acme/reports" with key "q1.pdf" and tenant "acme" with key
	// "reports/q1.pdf" resolved to the same object, each holding a valid Grant.
	// No Policy was violated: the path is built after the Policy runs.
	//
	// security.SystemGrant refuses an invalid tenant now, but a Grant built by
	// Authorize carries whatever the session holds, so the check belongs here
	// too -- this is the line that turns a tenant into a namespace.
	if !security.ValidTenant(tenant) {
		return "", fmt.Errorf("%w: %q cannot be a path segment", ErrNoTenant, tenant)
	}
	clean, err := CleanKey(key)
	if err != nil {
		return "", err
	}
	return tenant + "/" + clean, nil
}

// CleanKey normalizes a key and refuses one that would escape.
//
// This is the check that matters. A key is often a filename that came from an
// upload, and "../../../etc/passwd" is what an upload form eventually receives.
// Rejecting rather than sanitizing: a key that had to be rewritten to be safe is
// a key the caller did not mean, and silently storing it somewhere else is worse
// than an error.
func CleanKey(key string) (string, error) {
	if key == "" {
		return "", ErrBadKey
	}
	// A leading slash would make path.Clean produce an absolute path, which a
	// driver would then join into the wrong place.
	trimmed := strings.TrimPrefix(key, "/")
	clean := path.Clean(trimmed)

	if clean == "." || clean == "/" || strings.HasPrefix(clean, "../") || clean == ".." {
		return "", ErrBadKey
	}
	if strings.HasPrefix(clean, "/") {
		return "", ErrBadKey
	}
	// A NUL byte truncates a path in every syscall that takes one, so a key
	// carrying it can name a different file than it appears to.
	if strings.ContainsRune(clean, 0) {
		return "", ErrBadKey
	}
	return clean, nil
}
