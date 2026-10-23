package browser

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func factoryNamed(name string) Factory {
	return func(ctx context.Context, opts Options) (Driver, error) {
		return nil, errors.New(name)
	}
}

func TestRegistrySelectsByEnvironment(t *testing.T) {
	reg := Registry{"cdp": factoryNamed("cdp"), "playwright": factoryNamed("playwright")}

	t.Setenv("ARANDU_E2E_DRIVER", "")
	if name, _, err := reg.Select(); err != nil || name != "cdp" {
		t.Fatalf("default = %q, %v", name, err)
	}
	t.Setenv("ARANDU_E2E_DRIVER", "playwright")
	name, f, err := reg.Select()
	if err != nil || name != "playwright" {
		t.Fatalf("named = %q, %v", name, err)
	}
	if _, err := f(context.Background(), Options{}); err == nil || err.Error() != "playwright" {
		t.Fatalf("Select returned another factory: %v", err)
	}
	t.Setenv("ARANDU_E2E_DRIVER", "selenium")
	if _, _, err := reg.Select(); err == nil || !strings.Contains(err.Error(), `no driver "selenium" is registered (registered: cdp, playwright)`) {
		t.Fatalf("unknown = %v", err)
	}

	t.Setenv("ARANDU_E2E_DRIVER", "")
	only := Registry{"playwright": factoryNamed("playwright")}
	if name, _, err := only.Select(); err != nil || name != "playwright" {
		t.Fatalf("the only registered driver = %q, %v", name, err)
	}
	if _, _, err := (Registry{}).Select(); err == nil {
		t.Fatal("an empty registry selected something")
	}
}

func TestOptionsFromEnv(t *testing.T) {
	t.Setenv("ARANDU_E2E_BROWSER", "")
	t.Setenv("ARANDU_E2E_HEADED", "")
	t.Setenv("ARANDU_E2E_SLOWMO", "")
	opts, err := OptionsFromEnv()
	if err != nil || opts != (Options{Browser: "chromium"}) {
		t.Fatalf("defaults = %+v, %v", opts, err)
	}
	t.Setenv("ARANDU_E2E_BROWSER", "WebKit")
	t.Setenv("ARANDU_E2E_HEADED", "1")
	t.Setenv("ARANDU_E2E_SLOWMO", "250")
	opts, err = OptionsFromEnv()
	if err != nil || opts != (Options{Browser: "webkit", Headed: true, SlowMo: 250 * time.Millisecond}) {
		t.Fatalf("set = %+v, %v", opts, err)
	}
	t.Setenv("ARANDU_E2E_SLOWMO", "1.5s")
	if opts, _ := OptionsFromEnv(); opts.SlowMo != 1500*time.Millisecond {
		t.Fatalf("duration = %s", opts.SlowMo)
	}
	t.Setenv("ARANDU_E2E_SLOWMO", "slow")
	if _, err := OptionsFromEnv(); err == nil {
		t.Fatal("a slow motion that is not a duration was accepted")
	}
}

func TestLaunchSkipsWithoutABrowserAndFailsInCI(t *testing.T) {
	missing := func(ctx context.Context, opts Options) (Driver, error) {
		return nil, fmt.Errorf("%w: looked in /nowhere", ErrNoBrowser)
	}
	t.Setenv("CI", "")
	c := run(t, func(c *captured) { Launch(c, missing, Options{}) })
	if !c.skipped || c.failed || !strings.Contains(c.text(), "looked in /nowhere") {
		t.Fatalf("locally: skipped %t, failed %t, %q", c.skipped, c.failed, c.text())
	}

	t.Setenv("CI", "true")
	c = run(t, func(c *captured) { Launch(c, missing, Options{}) })
	if c.skipped || !c.failed || !strings.Contains(c.text(), "looked in /nowhere") {
		t.Fatalf("in CI: skipped %t, failed %t, %q", c.skipped, c.failed, c.text())
	}

	t.Setenv("CI", "")
	broken := func(ctx context.Context, opts Options) (Driver, error) { return nil, errors.New("crashed") }
	c = run(t, func(c *captured) { Launch(c, broken, Options{}) })
	if c.skipped || !c.failed {
		t.Fatalf("a browser that failed to start must fail, not skip: %q", c.text())
	}
}

func TestLaunchClosesTheDriverWhenTheTestEnds(t *testing.T) {
	f := &fakeDriver{}
	c := run(t, func(c *captured) {
		Launch(c, func(ctx context.Context, opts Options) (Driver, error) { return f, nil }, Options{})
	})
	if c.Failed() || !f.closed {
		t.Fatalf("closed %t: %s", f.closed, c.text())
	}
}

func TestOpenInstallsThePageScriptOnTheSelectedDriver(t *testing.T) {
	t.Setenv("ARANDU_E2E_DRIVER", "")
	t.Setenv("ARANDU_E2E_SLOWMO", "")
	f := &fakeDriver{}
	c := run(t, func(c *captured) {
		Open(c, Registry{"fake": func(ctx context.Context, opts Options) (Driver, error) { return f, nil }})
	})
	if c.Failed() || f.scripts != 1 || !f.closed {
		t.Fatalf("scripts %d, closed %t: %s", f.scripts, f.closed, c.text())
	}
}

// The kit waits by polling the page and the driver's events under a deadline.
// A sleep in it would be a wait that is either too short on a slow machine or
// too long on a fast one, so none may appear in these packages, tests
// included.
func TestNoSleepAnywhereInTheKit(t *testing.T) {
	forbidden := "time." + "Sleep"
	for _, root := range []string{".", filepath.Join("..", "trace")} {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(data), forbidden) {
				t.Errorf("%s calls %s; wait on an event or poll under a deadline instead", path, forbidden)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
