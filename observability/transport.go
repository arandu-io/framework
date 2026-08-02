package observability

import (
	"net/http"
	"time"
)

// Transport records every outbound call on the request's Collector.
//
// Without it, "external" on the timeline is always zero and the console shows
// nothing about the API the handler waited on -- which is the wrong answer for
// the request whose 800ms were spent in somebody else's service.
//
// Wrap the transport of the client the application uses:
//
//	client := &http.Client{
//	    Timeout:   10 * time.Second,
//	    Transport: observability.Transport(nil),
//	}
//
// It costs nothing in production for the same reason everything else here does:
// with no Collector in the context, RecordExternal returns on a nil receiver.
func Transport(next http.RoundTripper) http.RoundTripper {
	if next == nil {
		next = http.DefaultTransport
	}
	return roundTripper{next: next}
}

// Client returns an http.Client that records what it calls.
//
// The timeout is required rather than optional: http.Client has none by
// default, and a call with no deadline is how one slow dependency turns into
// every request of the process hanging.
func Client(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: Transport(nil)}
}

type roundTripper struct{ next http.RoundTripper }

func (t roundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	start := time.Now()
	resp, err := t.next.RoundTrip(r)

	status := 0
	if resp != nil {
		status = resp.StatusCode
	}
	// A URL can carry a token in the query string, and the console is a page
	// somebody screenshots. The path stays; the query does not.
	FromContext(r.Context()).RecordExternal(r.Method, redactURL(r), status, time.Since(start))

	return resp, err
}

// redactURL keeps scheme, host and path.
func redactURL(r *http.Request) string {
	if r.URL == nil {
		return ""
	}
	u := *r.URL
	u.RawQuery = ""
	u.Fragment = ""
	u.User = nil
	return u.String()
}
