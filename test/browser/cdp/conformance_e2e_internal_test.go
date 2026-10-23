//go:build e2e

package cdp

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/framework/test/browser"

	"github.com/arandu-io/framework/test/browser/conformance"
)

// TestConformance runs the suite every driver passes against Chrome.
func TestConformance(t *testing.T) {
	conformance.Run(t, Launch)
}

// TestCloseLeavesNothingBehind proves the browser exits and its profile
// directory goes with it.
func TestCloseLeavesNothingBehind(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	launched, err := Launch(ctx, browser.Options{})
	if errors.Is(err, browser.ErrNoBrowser) && os.Getenv("CI") == "" {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	d := launched.(*Driver)
	if info := d.Info(); info.Driver != "cdp" || !strings.Contains(info.Version, "Chrome") {
		t.Fatalf("info = %+v", info)
	}
	dir := d.profileDir()
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("the profile directory is not there while the browser runs: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("the profile directory survived Close: %v", err)
	}
	select {
	case <-d.exited:
	default:
		t.Fatal("the browser process is still running after Close")
	}
	if err := d.Evaluate(ctx, "1", nil); err == nil {
		t.Fatal("a closed driver still answered")
	}
	if err := d.Close(); err != nil {
		t.Fatalf("a second Close: %v", err)
	}
}
