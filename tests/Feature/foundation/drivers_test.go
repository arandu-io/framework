package feature

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arandu-io/framework/foundation"
	"github.com/arandu-io/framework/mail"
	"github.com/arandu-io/hesape/queue"
)

// TestTheDefaultDriversNeedNothingInstalled: a new project builds every driver
// with no server beside it, and nothing it builds pretends to be shared.
func TestTheDefaultDriversNeedNothingInstalled(t *testing.T) {
	d, err := foundation.NewDriversOver(settingsFor(t), pingDB(t))
	if err != nil {
		t.Fatalf("NewDriversOver: %v", err)
	}

	if d.SharedCache != nil || d.Locks != nil {
		t.Errorf("the in-process store was handed out as shared: SharedCache %v, Locks %v", d.SharedCache, d.Locks)
	}
	// An untyped nil, so whatever compares it with nil -- the relay, the
	// scheduler -- reads that nothing is shared.
	if d.Locker != nil {
		t.Errorf("Locker = %#v, want an untyped nil", d.Locker)
	}
	if d.Store == nil || d.Limiter == nil || d.Idempotency == nil {
		t.Error("the default store, the limiter or the idempotency store is missing")
	}
	if d.Sessions == nil || d.CSRF == nil {
		t.Error("the session store or the CSRF issuer is missing")
	}
	if _, ok := d.Queue.(*queue.DatabaseQueue); !ok {
		t.Errorf("Queue = %T, want the table in the application's own database", d.Queue)
	}
	if _, ok := d.Mail.Transport().(mail.Log); !ok {
		t.Errorf("transport = %T, want the log transport", d.Mail.Transport())
	}
	if _, err := d.Cache.Store("memory"); err != nil {
		t.Errorf("the memory store is not defined: %v", err)
	}
	if _, err := d.Cache.Store("redis"); err == nil {
		t.Error("the RESP store is defined with no REDIS_URL, so naming it would start a process that shares nothing")
	}
}

// TestTheMailURLPicksTheTransport: the scheme is the transport, and the
// transport is the framework's to build.
func TestTheMailURLPicksTheTransport(t *testing.T) {
	for _, c := range []struct {
		url   string
		check func(mail.Transport) bool
	}{
		{"array://", func(tr mail.Transport) bool { _, ok := tr.(*mail.Array); return ok }},
		{"resend://re_key", func(tr mail.Transport) bool { r, ok := tr.(mail.Resend); return ok && r.Key == "re_key" }},
		{"sendgrid://SG.key", func(tr mail.Transport) bool { r, ok := tr.(mail.SendGrid); return ok && r.Key == "SG.key" }},
		{"smtp://u:p@smtp.example.test:2525", func(tr mail.Transport) bool {
			s, ok := tr.(mail.SMTP)
			return ok && s.Host == "smtp.example.test" && s.Port == "2525" && s.Username == "u" && s.Password == "p"
		}},
	} {
		t.Run(c.url, func(t *testing.T) {
			d, err := foundation.NewDriversOver(settingsFor(t, "MAIL_URL", c.url), pingDB(t))
			if err != nil {
				t.Fatalf("NewDriversOver: %v", err)
			}
			if !c.check(d.Mail.Transport()) {
				t.Errorf("MAIL_URL=%s built %#v", c.url, d.Mail.Transport())
			}
		})
	}
}

// TestADriverThisBinaryDoesNotLinkStopsTheBoot: a setting naming the RESP
// connector in a binary that did not import it is refused naming the setting
// that asked and the import that adds it, rather than building something else.
func TestADriverThisBinaryDoesNotLinkStopsTheBoot(t *testing.T) {
	for _, c := range []struct {
		name    string
		env     []string
		setting string
	}{
		{"the cache", []string{"CACHE_STORE", "redis", "REDIS_URL", "redis://127.0.0.1:6379"}, "CACHE_STORE"},
		{"the sessions", []string{"SESSION_DRIVER", "redis", "REDIS_URL", "redis://127.0.0.1:6379"}, "SESSION_DRIVER"},
		{"the queue", []string{"QUEUE_CONNECTION", "redis", "REDIS_URL", "redis://127.0.0.1:6379"}, "QUEUE_CONNECTION"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := foundation.NewDriversOver(settingsFor(t, c.env...), pingDB(t))
			if err == nil {
				t.Fatal("a driver this binary does not link was accepted")
			}
			if !strings.Contains(err.Error(), c.setting) || !strings.Contains(err.Error(), "github.com/arandu-io/hesape/") {
				t.Errorf("error = %q, want it to name %s and the import that links the connector", err, c.setting)
			}
		})
	}
}

// TestACertificateThatCannotBeReadStopsTheBoot: encryption that was asked for
// and cannot be set up is a boot that stops, never a connection in the clear.
func TestACertificateThatCannotBeReadStopsTheBoot(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.pem")
	for _, c := range []struct {
		name string
		env  []string
		want string
	}{
		{"certificates for a connection in the clear", []string{"REDIS_URL", "redis://127.0.0.1:6379", "REDIS_CA_FILE", missing}, "rediss://"},
		{"an authority that is not there", []string{"REDIS_URL", "rediss://127.0.0.1:6380", "REDIS_CA_FILE", missing}, "REDIS_CA_FILE"},
		{"half a client pair", []string{"REDIS_URL", "rediss://127.0.0.1:6380", "REDIS_CERT_FILE", missing}, "REDIS_KEY_FILE"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := foundation.NewDriversOver(settingsFor(t, c.env...), pingDB(t))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %v, want it to name %s", err, c.want)
			}
		})
	}
}

// TestTheDatabaseIsOpenedByTheAdapter: NewDrivers opens DATABASE_URL through
// the adapter, so an engine this binary does not link is the adapter's refusal
// with the import that links it, and nothing is left open behind it.
func TestTheDatabaseIsOpenedByTheAdapter(t *testing.T) {
	_, err := foundation.NewDrivers(settingsFor(t))
	if err == nil || !strings.Contains(err.Error(), "github.com/arandu-io/hesape/database/connectors/sqlite") {
		t.Errorf("error = %v, want the adapter's refusal naming the SQLite connector", err)
	}

	if _, err := foundation.NewDriversOver(settingsFor(t), nil); err == nil {
		t.Error("drivers were built over no database")
	}
}

// TestClosingTheDriversTwiceIsHarmless: Shutdown and the end of a command both
// close them, and the second close must not reach a database already closed.
func TestClosingTheDriversTwiceIsHarmless(t *testing.T) {
	d, err := foundation.NewDriversOver(settingsFor(t), pingDB(t))
	if err != nil {
		t.Fatalf("NewDriversOver: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d.DB.PingContext(context.Background()); err != nil {
		t.Errorf("drivers built over the caller's database closed it: %v", err)
	}
}

// TestTheLocalDiskIsTheDefaultAndPrivate: with nothing configured, files are
// kept under FILESYSTEM_ROOT, on the one disk the configuration named.
func TestTheLocalDiskIsTheDefaultAndPrivate(t *testing.T) {
	root := filepath.Join(t.TempDir(), "files")
	d, err := foundation.NewDriversOver(settingsFor(t, "FILESYSTEM_ROOT", root), pingDB(t))
	if err != nil {
		t.Fatalf("NewDriversOver: %v", err)
	}
	disk, err := d.Disks.Disk("")
	if err != nil {
		t.Fatalf("the default disk: %v", err)
	}
	if disk.Name() != "local" || strings.Join(d.Disks.Names(), ",") != "local" {
		t.Errorf("default disk %q among %v, want local alone", disk.Name(), d.Disks.Names())
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		t.Errorf("the local root %s was not created: %v", root, err)
	}
	if _, err := d.Disks.Cloud(); err == nil {
		t.Error("a local disk was handed out as the cloud disk")
	}
}

// TestABucketDiskIsBuiltFromItsConfiguration: FILESYSTEM_DISK=r2 is an
// object storage disk built by the framework from the four variables, with no
// directory created for a local disk nobody asked for and nothing dialled.
func TestABucketDiskIsBuiltFromItsConfiguration(t *testing.T) {
	root := filepath.Join(t.TempDir(), "never")
	for _, name := range []string{"r2", "s3"} {
		t.Run(name, func(t *testing.T) {
			d, err := foundation.NewDriversOver(settingsFor(t,
				"FILESYSTEM_DISK", name,
				"FILESYSTEM_ROOT", root,
				"FILESYSTEM_BUCKET", "kuaa-dev",
				"FILESYSTEM_ENDPOINT", "https://account.r2.cloudflarestorage.com",
				"FILESYSTEM_ACCESS_KEY_ID", "key",
				"FILESYSTEM_SECRET_ACCESS_KEY", "secret",
			), pingDB(t))
			if err != nil {
				t.Fatalf("NewDriversOver: %v", err)
			}
			disk, err := d.Disks.Disk("")
			if err != nil || disk.Name() != name {
				t.Fatalf("default disk = %v, %v; want %s", disk, err, name)
			}
			if cloud, err := d.Disks.Cloud(); err != nil || cloud.Name() != name {
				t.Errorf("cloud disk = %v, %v; want %s", cloud, err, name)
			}
			if _, err := os.Stat(root); !os.IsNotExist(err) {
				t.Errorf("a bucket deployment created the local root %s", root)
			}
		})
	}
}
