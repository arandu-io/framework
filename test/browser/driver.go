package browser

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Driver is one browser engine driving one page in a browsing context of its
// own: its cookies, storage and cache are seen by no other Driver. It is what a
// browser does and nothing more -- the waiting, the locating and the failing
// live in Page, on top of any Driver, so every engine behaves the same.
//
// Methods are called from one goroutine at a time. The function handed to
// Listen is called from the driver's own goroutine.
type Driver interface {
	// Info names the engine and the browser behind it.
	Info() Info

	// Navigate loads url and returns once the document's load event fired. A
	// navigation the network refused is an error carrying the browser's text.
	Navigate(ctx context.Context, url string) error

	// Evaluate runs a JavaScript expression in the page's main frame, awaits it
	// when it is a promise, and decodes its JSON value into result, which may
	// be nil. An exception thrown by the expression is an error carrying its
	// message. An expression interrupted because the document went away is an
	// error wrapping ErrDocumentChanged.
	Evaluate(ctx context.Context, expression string, result any) error

	// AddInitScript runs source in every document loaded from now on, before
	// any of the document's own scripts and regardless of its CSP.
	AddInitScript(ctx context.Context, source string) error

	// Click moves the primary pointer to x, y in viewport pixels, presses it
	// and releases it, as a person clicking does.
	Click(ctx context.Context, x, y float64) error

	// InsertText types text into the focused element as one input.
	InsertText(ctx context.Context, text string) error

	// Press presses and releases one key: a key name such as "Enter", "Tab",
	// "Escape" or "ArrowDown", or a single character, optionally prefixed by
	// modifiers as in "Shift+Tab".
	Press(ctx context.Context, key string) error

	// SetViewport resizes the page's viewport.
	SetViewport(ctx context.Context, v Viewport) error

	// EmulateMedia sets the media type and the user preferences media queries
	// read.
	EmulateMedia(ctx context.Context, m Media) error

	// Screenshot captures the viewport as PNG.
	Screenshot(ctx context.Context) ([]byte, error)

	// SetCookies stores cookies in this browsing context as if url had set
	// them; HttpOnly cookies included, which a script cannot write.
	SetCookies(ctx context.Context, url string, cookies []*http.Cookie) error

	// Cookies returns the cookies this browsing context would send to url.
	Cookies(ctx context.Context, url string) ([]*http.Cookie, error)

	// Listen hands every event from now on to fn. It is called once, before
	// the first navigation. fn must not block and must not call the driver.
	Listen(fn func(Event))

	// Close ends the browsing context and whatever process the driver started
	// for it.
	Close() error
}

// Info names a driver and its browser.
type Info struct {
	// Driver is the name the driver is registered under, such as "cdp".
	Driver string
	// Browser is the engine: "chromium", "firefox" or "webkit".
	Browser string
	// Version is the browser's own version string, when the driver knows it.
	Version string
}

// Viewport is the size of the page's visible area in CSS pixels.
type Viewport struct {
	Width, Height int
	// Scale is the device pixel ratio; zero means 1.
	Scale float64
	// Mobile emulates a mobile device's viewport meta handling.
	Mobile bool
}

// Media is what CSS media queries see.
type Media struct {
	// Type is "screen" or "print"; empty means screen.
	Type string
	// ColorScheme is prefers-color-scheme: "light" or "dark".
	ColorScheme string
	// ReducedMotion is prefers-reduced-motion: "no-preference" or "reduce".
	ReducedMotion string
}

// EventKind says what an Event reports.
type EventKind string

// The events a driver reports.
const (
	// EventConsole is a console message, or the browser's own log entry about
	// the page. Level is "error", "warning", "info", "log" or "debug"; Source
	// is "console-api" for a call to console.* and the browser's own source
	// name (such as "network" or "security") otherwise.
	EventConsole EventKind = "console"
	// EventException is an exception the page did not catch, including an
	// unhandled promise rejection.
	EventException EventKind = "exception"
	// EventRequest is a request leaving the page.
	EventRequest EventKind = "request"
	// EventResponse is the status and headers of the answer to a request.
	EventResponse EventKind = "response"
	// EventRequestFinished is a request whose body has been read.
	EventRequestFinished EventKind = "request-finished"
	// EventRequestFailed is a request that ended without an answer; Text is
	// the browser's reason.
	EventRequestFailed EventKind = "request-failed"
	// EventNavigationStarted is the main frame starting to load a document.
	EventNavigationStarted EventKind = "navigation-started"
	// EventNavigationFinished is the main frame done loading, whether the
	// document loaded or the navigation was abandoned.
	EventNavigationFinished EventKind = "navigation-finished"
)

// Event is something the page did that the test did not ask for directly.
type Event struct {
	Kind EventKind
	Time time.Time

	// Level and Source qualify a console event.
	Level, Source string
	// Text is the message, the exception or the failure reason.
	Text string

	// RequestID ties request, response, finished and failed together. It is
	// the driver's id, not the server's.
	RequestID string
	Method    string
	URL       string
	// ResourceType is the kind of request: "Document", "XHR", "Fetch",
	// "Script", "Stylesheet", "Image", "EventSource", "WebSocket", ...
	ResourceType string
	Status       int
	Header       http.Header
}

// ErrNoBrowser is wrapped by a Factory that found no browser to start. Launch
// turns it into a skip on a developer's machine and a failure in CI.
var ErrNoBrowser = errors.New("browser: no browser was found")

// ErrDocumentChanged is wrapped by Evaluate when the document the expression
// ran in went away under it, as it does during a navigation. The page treats it
// as "not yet" while it waits.
var ErrDocumentChanged = errors.New("browser: the document changed during the evaluation")

// Options is what a Factory is launched with. OptionsFromEnv reads them from
// the environment.
type Options struct {
	// Browser is the engine wanted: "chromium" (the default), "firefox" or
	// "webkit". A driver refuses one it cannot drive.
	Browser string
	// Headed shows the browser window.
	Headed bool
	// SlowMo delays every navigation and input by this much, for watching a
	// headed run.
	SlowMo time.Duration
}

// Factory launches a Driver. Each call returns a browsing context isolated from
// every other one the Factory returned.
type Factory func(ctx context.Context, opts Options) (Driver, error)

// Registry is the drivers an application can run its browser tests with, by
// name. It is a value the application writes, in one line of its test case:
//
//	var Drivers = browser.Registry{"cdp": cdp.Launch}
//
// There is no registration by import and no package-level list: the drivers a
// test can run with are the ones written there.
type Registry map[string]Factory

// DefaultDriver is the driver chosen when ARANDU_E2E_DRIVER is empty and the
// registry holds more than one.
const DefaultDriver = "cdp"

// Select returns the driver ARANDU_E2E_DRIVER names. With the variable empty it
// is the only one registered, or DefaultDriver.
func (r Registry) Select() (string, Factory, error) {
	name := strings.TrimSpace(os.Getenv("ARANDU_E2E_DRIVER"))
	if name == "" {
		if len(r) == 1 {
			for only, f := range r {
				return only, f, nil
			}
		}
		name = DefaultDriver
	}
	if f, ok := r[name]; ok && f != nil {
		return name, f, nil
	}
	names := make([]string, 0, len(r))
	for n := range r {
		names = append(names, n)
	}
	sort.Strings(names)
	return "", nil, fmt.Errorf("browser: no driver %q is registered (registered: %s); ARANDU_E2E_DRIVER picks one of them",
		name, strings.Join(names, ", "))
}

// OptionsFromEnv reads ARANDU_E2E_BROWSER, ARANDU_E2E_HEADED and
// ARANDU_E2E_SLOWMO. SlowMo is a Go duration ("250ms") or a number of
// milliseconds ("250").
func OptionsFromEnv() (Options, error) {
	opts := Options{Browser: strings.ToLower(strings.TrimSpace(os.Getenv("ARANDU_E2E_BROWSER")))}
	if opts.Browser == "" {
		opts.Browser = "chromium"
	}
	opts.Headed = truthy(os.Getenv("ARANDU_E2E_HEADED"))
	if raw := strings.TrimSpace(os.Getenv("ARANDU_E2E_SLOWMO")); raw != "" {
		if ms, err := strconv.Atoi(raw); err == nil {
			opts.SlowMo = time.Duration(ms) * time.Millisecond
		} else if d, err := time.ParseDuration(raw); err == nil {
			opts.SlowMo = d
		} else {
			return Options{}, fmt.Errorf("browser: ARANDU_E2E_SLOWMO=%q is neither a duration nor a number of milliseconds", raw)
		}
	}
	return opts, nil
}

// Launch starts a driver for the test and closes it when the test ends. A
// Factory that found no browser skips the test, naming where it looked -- and
// fails it when CI is set, because a browser suite that skips in CI is a suite
// that never runs.
func Launch(t testing.TB, factory Factory, opts Options) Driver {
	t.Helper()
	d, err := factory(t.Context(), opts)
	if err != nil {
		if errors.Is(err, ErrNoBrowser) && os.Getenv("CI") == "" {
			t.Skipf("%v", err)
		}
		t.Fatalf("browser: launch: %v", err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Errorf("browser: close: %v", err)
		}
	})
	return d
}

func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
