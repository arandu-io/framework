package foundation

import (
	"fmt"

	"github.com/arandu-io/framework/foundation/bootstrap"
	"github.com/arandu-io/hesape/filesystem"
	"github.com/arandu-io/hesape/filesystem/s3"
)

// newDisks builds the disk FILESYSTEM_DISK named and makes it the default.
//
// One disk, the one the configuration asked for. A bucket disk is built over
// the S3 protocol from the bucket, the endpoint, the region and the
// credentials, with the bucket in the path for R2, which wants it there; it is
// also the cloud disk. The local disk is built only when it is the one named,
// because building it creates its root, and a container whose files live in a
// bucket may have no directory it can write.
//
// Both are private: a file is reached through a handler that checked a Grant,
// and what is deliberately shared is shared by a signed URL.
func newDisks(f bootstrap.Filesystems) (*filesystem.Disks, error) {
	name := string(f.Default)
	if !f.IsObjectStorage() {
		disks := filesystem.NewDisks(name)
		err := disks.Configure(map[string]filesystem.Config{
			name: {
				Driver:      "local",
				Root:        f.Root,
				Visibility:  filesystem.VisibilityPrivate,
				ServeSigned: true,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("foundation: building the %s disk under %s: %w", name, f.Root, err)
		}
		return disks, nil
	}

	disks := filesystem.NewDisks(name, name)
	disks.Extend(s3.DriverName, s3.Driver())
	err := disks.Configure(map[string]filesystem.Config{
		name: {
			Driver:     s3.DriverName,
			Bucket:     f.Bucket,
			Endpoint:   f.Endpoint,
			Region:     f.Region,
			AccessKey:  f.AccessKeyID,
			SecretKey:  f.SecretAccessKey,
			PathStyle:  f.Default == bootstrap.DiskR2,
			Visibility: filesystem.VisibilityPrivate,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("foundation: building the %s disk: %w", name, err)
	}
	return disks, nil
}
