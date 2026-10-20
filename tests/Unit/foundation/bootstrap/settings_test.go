package unit

import (
	"fmt"
	"net/netip"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/framework/foundation/bootstrap"
)

// settingsWith loads the sections from a clean environment plus the values
// given, from a directory with no .env in it.
func settingsWith(t *testing.T, values map[string]string) (bootstrap.Settings, error) {
	t.Helper()
	t.Chdir(t.TempDir())
	t.Setenv("APP_ENV", "dev")
	t.Setenv("APP_KEY", testKey)
	t.Setenv("DATABASE_URL", "sqlite://"+filepath.Join(t.TempDir(), "test.sqlite"))
	for _, key := range []string{
		"APP_URL", "CACHE_STORE", "CACHE_PREFIX", "CACHE_TTL", "REDIS_URL",
		"REDIS_CA_FILE", "REDIS_CERT_FILE", "REDIS_KEY_FILE", "REDIS_TLS_SERVER_NAME",
		"SESSION_DRIVER", "SESSION_SECURE", "CSRF_TTL",
		"QUEUE_CONNECTION", "QUEUE_DEFAULT", "QUEUE_WORKERS", "QUEUE_RETRY_AFTER", "QUEUE_MAX_ATTEMPTS",
		"FILESYSTEM_DISK", "FILESYSTEM_ROOT", "FILESYSTEM_BUCKET", "FILESYSTEM_ENDPOINT",
		"FILESYSTEM_REGION", "FILESYSTEM_ACCESS_KEY_ID", "FILESYSTEM_SECRET_ACCESS_KEY",
		"LOG_FORMAT", "LOG_SAMPLING", "HTTP_MAX_BODY_BYTES", "TRUSTED_PROXIES",
		"MAIL_URL", "MAIL_FROM_ADDRESS", "MAIL_FROM_NAME",
		"MAIL_MAILER", "MAIL_HOST", "MAIL_PORT", "MAIL_USERNAME", "MAIL_PASSWORD", "MAIL_ENCRYPTION", "MAIL_KEY",
	} {
		t.Setenv(key, "")
	}
	for key, value := range values {
		t.Setenv(key, value)
	}
	return bootstrap.LoadSettings()
}

// TestTheSectionsDefaultToWhatRunsWithNothingInstalled: a new project starts
// with nothing beside it -- no server for the cache, the sessions or the
// queue, no mail provider, no bucket -- and every default is the one that
// needs none.
func TestTheSectionsDefaultToWhatRunsWithNothingInstalled(t *testing.T) {
	s, err := settingsWith(t, nil)
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	checks := []struct {
		name      string
		got, want any
	}{
		{"cache store", s.Cache.Store, bootstrap.CacheMemory},
		{"cache ttl", s.Cache.TTL, 10 * time.Minute},
		{"cache prefix", s.Cache.Prefix, s.App.Name + ":cache:"},
		{"session driver", s.Session.Driver, bootstrap.SessionMemory},
		{"csrf ttl", s.Session.CSRFTTL, 2 * time.Hour},
		{"queue connection", s.Queue.Connection, bootstrap.QueueDatabase},
		{"queue workers", s.Queue.Workers, 0},
		{"queue lease", s.Queue.RetryAfter, time.Duration(0)},
		{"disk", s.Filesystems.Default, bootstrap.DiskLocal},
		{"disk root", s.Filesystems.Root, "storage/app/private"},
		{"region", s.Filesystems.Region, "auto"},
		{"log format", s.Logging.Format, "text"},
		{"body limit", s.HTTP.MaxBodyBytes, bootstrap.DefaultMaxBodyBytes},
		{"trusted proxies", len(s.HTTP.TrustedProxies), 0},
		{"mailer", s.Mail.Mailer, bootstrap.MailerLog},
		{"sender", s.Mail.FromAddress, "no-reply@localhost"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	if !reflect.DeepEqual(s.Database.Connection, s.Framework.Database) {
		t.Error("the database section is not the connection the kernel parsed")
	}
	if s.Logging.Level != s.Framework.Observability.LogLevel {
		t.Error("the log level is read twice")
	}
	if !s.App.IsDev() || s.App.Name != s.Framework.App.Name {
		t.Errorf("App = %+v, want a view of the kernel's application in dev", s.App)
	}
}

// TestTheSectionsReadWhatTheyAreGiven: each variable reaches its field, in the
// unit its name says.
func TestTheSectionsReadWhatTheyAreGiven(t *testing.T) {
	s, err := settingsWith(t, map[string]string{
		"CACHE_STORE":                  "redis",
		"REDIS_URL":                    "rediss://:secret@cache.example.test:6380/2",
		"CACHE_TTL":                    "60",
		"SESSION_DRIVER":               "redis",
		"CSRF_TTL":                     "300",
		"QUEUE_CONNECTION":             "redis",
		"QUEUE_DEFAULT":                "emails",
		"QUEUE_WORKERS":                "4",
		"QUEUE_RETRY_AFTER":            "120",
		"QUEUE_MAX_ATTEMPTS":           "5",
		"FILESYSTEM_DISK":              "r2",
		"FILESYSTEM_BUCKET":            "kuaa-dev",
		"FILESYSTEM_ENDPOINT":          "https://account.r2.cloudflarestorage.com/",
		"FILESYSTEM_ACCESS_KEY_ID":     "key",
		"FILESYSTEM_SECRET_ACCESS_KEY": "secret",
		"LOG_FORMAT":                   "json",
		"HTTP_MAX_BODY_BYTES":          "1024",
		"TRUSTED_PROXIES":              "10.0.0.0/8, 192.168.1.7",
		"MAIL_URL":                     "smtps://user:pass@smtp.example.test",
	})
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if s.Cache.Address != "cache.example.test:6380" || s.Cache.Password != "secret" || s.Cache.Database != 2 || !s.Cache.TLS {
		t.Errorf("cache endpoint = %+v", s.Cache)
	}
	if s.Cache.TTL != time.Minute || s.Session.CSRFTTL != 5*time.Minute {
		t.Errorf("durations = %v, %v; want seconds", s.Cache.TTL, s.Session.CSRFTTL)
	}
	if s.Queue.Default != "emails" || s.Queue.Workers != 4 || s.Queue.RetryAfter != 2*time.Minute || s.Queue.MaxAttempts != 5 {
		t.Errorf("queue = %+v", s.Queue)
	}
	if s.Filesystems.Endpoint != "https://account.r2.cloudflarestorage.com" {
		t.Errorf("endpoint = %q, want it without the trailing slash", s.Filesystems.Endpoint)
	}
	want := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("192.168.1.7/32")}
	if fmt.Sprint(s.HTTP.TrustedProxies) != fmt.Sprint(want) || s.HTTP.MaxBodyBytes != 1024 {
		t.Errorf("http = %+v", s.HTTP)
	}
	if s.Mail.Mailer != bootstrap.MailerSMTP || s.Mail.Port != 465 || s.Mail.Encryption != "tls" || s.Mail.Username != "user" {
		t.Errorf("mail = %+v", s.Mail)
	}
}

// TestASectionThatCannotBeUsedStopsTheBootNamingItself: a value that is
// present and cannot be used is refused at the boot, with the variable's name
// and the accepted values in the sentence -- never replaced by a default the
// deployment did not choose.
func TestASectionThatCannotBeUsedStopsTheBootNamingItself(t *testing.T) {
	for _, c := range []struct {
		name string
		env  map[string]string
		want []string
	}{
		{"cache over RESP without its URL", map[string]string{"CACHE_STORE": "redis"}, []string{"CACHE_STORE", "REDIS_URL"}},
		{"sessions over RESP without its URL", map[string]string{"SESSION_DRIVER": "redis"}, []string{"SESSION_DRIVER", "REDIS_URL"}},
		{"queue over RESP without its URL", map[string]string{"QUEUE_CONNECTION": "redis"}, []string{"QUEUE_CONNECTION", "REDIS_URL"}},
		{"queue with a URL that is not RESP", map[string]string{"QUEUE_CONNECTION": "redis", "REDIS_URL": "memcached://x:1"}, []string{"REDIS_URL", `"memcached"`}},
		{"unknown cache store", map[string]string{"CACHE_STORE": "memcached"}, []string{"CACHE_STORE", "memory", "redis"}},
		{"unknown session driver", map[string]string{"SESSION_DRIVER": "database"}, []string{"SESSION_DRIVER", "memory", "redis"}},
		{"unknown queue connection", map[string]string{"QUEUE_CONNECTION": "sqs"}, []string{"QUEUE_CONNECTION", "database", "redis"}},
		{"unknown disk", map[string]string{"FILESYSTEM_DISK": "gcs"}, []string{"FILESYSTEM_DISK", "local", "r2", "s3"}},
		{"a bucket disk with nothing to reach it", map[string]string{"FILESYSTEM_DISK": "r2"},
			[]string{"FILESYSTEM_DISK=r2", "FILESYSTEM_ACCESS_KEY_ID", "FILESYSTEM_BUCKET", "FILESYSTEM_ENDPOINT", "FILESYSTEM_SECRET_ACCESS_KEY"}},
		{"unknown log format", map[string]string{"LOG_FORMAT": "yaml"}, []string{"LOG_FORMAT", "json", "text"}},
		{"retired mail variable", map[string]string{"MAIL_HOST": "smtp.example.test"}, []string{"MAIL_HOST", "MAIL_URL"}},
		{"unknown mail scheme", map[string]string{"MAIL_URL": "pigeon://coop"}, []string{"MAIL_URL", `"pigeon"`}},
		{"retired session variable", map[string]string{"SESSION_SECURE": "false"}, []string{"SESSION_SECURE", "SESSION_SECURE_COOKIE"}},
		{"a count of none", map[string]string{"QUEUE_WORKERS": "0"}, []string{"QUEUE_WORKERS", "positive"}},
		{"a body limit of none", map[string]string{"HTTP_MAX_BODY_BYTES": "0"}, []string{"HTTP_MAX_BODY_BYTES", "positive"}},
		{"every proxy trusted", map[string]string{"TRUSTED_PROXIES": "*"}, []string{"TRUSTED_PROXIES", `"*"`}},
		{"a RESP user nobody can become", map[string]string{"REDIS_URL": "redis://admin:x@cache:6379"}, []string{"REDIS_URL", `"admin"`}},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := settingsWith(t, c.env)
			if err == nil {
				t.Fatal("LoadSettings accepted it")
			}
			for _, want := range c.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to contain %q", err, want)
				}
			}
		})
	}
}

// TestSettingsFromRefusesABaseItCannotBuildOn: a Configuration built in Go
// skips the loader's checks, so the sections check what they build on.
func TestSettingsFromRefusesABaseItCannotBuildOn(t *testing.T) {
	if _, err := bootstrap.SettingsFrom(bootstrap.Configuration{}); err == nil {
		t.Error("an empty base was accepted")
	}

	s, err := settingsWith(t, nil)
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	base := s.Framework
	base.Session.Lifetime = 0
	_, err = bootstrap.SettingsFrom(base)
	if err == nil || !strings.Contains(err.Error(), "SESSION_LIFETIME") {
		t.Errorf("a base with no session lifetime answered %v, want the refusal naming SESSION_LIFETIME", err)
	}
}

// TestTheBucketCredentialsDoNotPrint: a section printed in a log line or an
// error does not carry the key or the secret.
func TestTheBucketCredentialsDoNotPrint(t *testing.T) {
	f := bootstrap.Filesystems{Default: bootstrap.DiskR2, AccessKeyID: "AKIA-visible", SecretAccessKey: "very-secret"}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		out := fmt.Sprintf(verb, f)
		if strings.Contains(out, "AKIA-visible") || strings.Contains(out, "very-secret") {
			t.Errorf("%s printed the credentials: %s", verb, out)
		}
	}
}
