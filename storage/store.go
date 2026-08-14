// The driver contract, answered by github.com/arandu-io/hesape/filesystem.
//
// This is the half of the move that reshaped rather than renamed. hesape split
// the old Store into filesystem.Adapter, which takes stored paths and no Grant,
// and filesystem.Disk, which takes a Grant and calls filesystem.Key before it
// calls the Adapter. The two names below therefore stay declared with their old
// form: github.com/arandu-io/storage and github.com/arandu-io/storage/s3
// implement Store and build File in separate modules, and an alias would compile
// here while breaking both of them in silence -- the one failure `go build` in
// this module cannot catch.

package storage

import (
	"context"
	"io"
	"time"

	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/filesystem"
)

// ErrNotFound is returned when a key does not exist for this tenant.
//
// It is the same error whether the file is absent or belongs to somebody else,
// and that is deliberate: distinguishing them would tell a caller which keys
// exist in other tenants.
//
// It is one value with filesystem.ErrNotFound, so a driver written against
// either name satisfies the other's contract without a line changing.
var ErrNotFound = filesystem.ErrNotFound

// File is what a Get returns.
//
// It stays declared here rather than aliasing filesystem.File, which carries the
// same four fields inside an embedded filesystem.Info. The fields and their
// meanings are identical, but the flat composite literal the two driver modules
// write does not compile against an embedded one, and a bridge that changes a
// shape is not a bridge.
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
// customer's files by building a string". Reads are not exempt -- List, Get and
// Exists take one for the same reason Put does (RULE 17), and a listing is the
// one call where forgetting it hands over the names of every file in the system.
//
// It stays declared here rather than aliasing filesystem.Adapter, which took the
// Grant off every method and added a sixth. The Grant did not disappear there:
// it moved up to filesystem.Disk, which resolves the path before the driver sees
// it. Here it is still the driver that calls Path, and Path is still the only
// thing that produces a stored path.
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
