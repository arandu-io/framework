package bootstrap

import (
	"fmt"
	"slices"
	"strings"
)

// Disk is where uploaded files live.
type Disk string

// The supported disks. Object storage first, because that is what a deployed
// application uses; local exists for development, for tests and for a single
// machine.
const (
	// DiskLocal writes under Filesystems.Root. One machine only: a second
	// replica does not see what the first one wrote.
	DiskLocal Disk = "local"
	// DiskR2 is Cloudflare R2: the S3 protocol, with the bucket in the path.
	DiskR2 Disk = "r2"
	// DiskS3 is Amazon S3 and anything else that speaks its API.
	DiskS3 Disk = "s3"
)

// Filesystems is where uploaded files live, and what it takes to reach them.
//
// There is no public disk and no symlink into one. An uploaded file is served
// by a handler that checked a Grant first; a directory the web server publishes
// is a directory with no policy in front of it.
type Filesystems struct {
	// Default is FILESYSTEM_DISK: local, the default, r2 or s3.
	Default Disk

	// Root is where the local disk writes: FILESYSTEM_ROOT, and
	// storage/app/private when it is not written.
	Root string

	// Bucket, Endpoint and Region describe the object store:
	// FILESYSTEM_BUCKET, FILESYSTEM_ENDPOINT -- the service without the bucket,
	// such as https://<account>.r2.cloudflarestorage.com -- and
	// FILESYSTEM_REGION, auto when it is not written, which is what R2 wants.
	Bucket   string
	Endpoint string
	Region   string

	// AccessKeyID and SecretAccessKey authenticate against it:
	// FILESYSTEM_ACCESS_KEY_ID and FILESYSTEM_SECRET_ACCESS_KEY. They come from
	// the environment and are never written to a file in a repository. String
	// and GoString print both as [redacted].
	AccessKeyID     string
	SecretAccessKey string
}

// IsObjectStorage reports whether the default disk is a bucket rather than a
// directory.
func (f Filesystems) IsObjectStorage() bool { return f.Default == DiskR2 || f.Default == DiskS3 }

// String describes the section with the credentials left out.
func (f Filesystems) String() string { return f.GoString() }

// GoString is String for the %#v verb, so no formatting verb prints the
// credentials.
func (f Filesystems) GoString() string {
	redacted := f
	if redacted.AccessKeyID != "" {
		redacted.AccessKeyID = "[redacted]"
	}
	if redacted.SecretAccessKey != "" {
		redacted.SecretAccessKey = "[redacted]"
	}
	type plain Filesystems
	return fmt.Sprintf("bootstrap.Filesystems%+v", plain(redacted))
}

func loadFilesystemSettings() (Filesystems, error) {
	disk := Disk(env("FILESYSTEM_DISK", string(DiskLocal)))
	switch disk {
	case DiskLocal, DiskR2, DiskS3:
	default:
		return Filesystems{}, fmt.Errorf("FILESYSTEM_DISK has unsupported value %q; expected local, r2 or s3", disk)
	}
	cfg := Filesystems{
		Default:         disk,
		Root:            env("FILESYSTEM_ROOT", "storage/app/private"),
		Bucket:          env("FILESYSTEM_BUCKET", ""),
		Endpoint:        strings.TrimRight(env("FILESYSTEM_ENDPOINT", ""), "/"),
		Region:          env("FILESYSTEM_REGION", "auto"),
		AccessKeyID:     env("FILESYSTEM_ACCESS_KEY_ID", ""),
		SecretAccessKey: env("FILESYSTEM_SECRET_ACCESS_KEY", ""),
	}

	// A bucket disk missing any of the four is refused here, naming every one
	// that is missing: the alternative is a boot that succeeds and a first
	// upload that fails in front of a customer.
	if cfg.IsObjectStorage() {
		var missing []string
		for name, value := range map[string]string{
			"FILESYSTEM_ENDPOINT":          cfg.Endpoint,
			"FILESYSTEM_BUCKET":            cfg.Bucket,
			"FILESYSTEM_ACCESS_KEY_ID":     cfg.AccessKeyID,
			"FILESYSTEM_SECRET_ACCESS_KEY": cfg.SecretAccessKey,
		} {
			if value == "" {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			slices.Sort(missing)
			return Filesystems{}, fmt.Errorf("FILESYSTEM_DISK=%s needs %s", disk, strings.Join(missing, ", "))
		}
	}
	return cfg, nil
}
