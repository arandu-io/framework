package bootstrap

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// The readers below are the only place the sections touch the environment. A
// setting read at the point of use is a setting nobody can list, and listing
// them is what the sections are for.

// env answers the variable, or fallback when it is unset or empty.
func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

// envInt reads a whole number, refusing one that does not parse.
func envInt(key string, fallback int) (int, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s has invalid integer value %q: %w", key, v, err)
	}
	return n, nil
}

// envCount reads a variable that counts something, and answers zero when it was
// not written at all.
//
// Zero is what the component downstream reads as "keep your own default", so no
// number is written here and the variable being absent is what asks for one. A
// value that is present and not positive is refused rather than read as the
// default, because there is no count of none to ask for -- QUEUE_WORKERS=0
// quietly restoring the default is an .env saying one thing and the process
// doing another with nothing reporting it.
func envCount(key string) (int, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return 0, nil
	}
	n, err := envInt(key, 0)
	if err != nil {
		return 0, err
	}
	if n <= 0 {
		return 0, fmt.Errorf("%s must be a positive whole number, got %q; leave it unset to keep the default", key, v)
	}
	return n, nil
}

// envSeconds reads a duration expressed in seconds, not in Go's duration
// syntax: these values are written by deployment tooling as often as by people,
// and "3600" travels through a chart better than "1h".
func envSeconds(key string, fallback time.Duration) (time.Duration, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback, nil
	}
	n, err := envInt(key, 0)
	if err != nil {
		return 0, err
	}
	if n <= 0 {
		return 0, fmt.Errorf("%s must be a positive number of seconds, got %q", key, v)
	}
	return time.Duration(n) * time.Second, nil
}

// envBytes reads a size in bytes. Zero and a negative are refused rather than
// read as "no limit": there is no unbounded size to ask for, and leaving the
// variable out is how the default is asked for.
func envBytes(key string, fallback int64) (int64, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback, nil
	}
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s has invalid integer value %q: %w", key, v, err)
	}
	if n <= 0 {
		return 0, fmt.Errorf("%s must be a positive number of bytes, got %q; leave it unset to keep the default", key, v)
	}
	return n, nil
}
