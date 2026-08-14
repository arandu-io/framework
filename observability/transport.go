// The outbound HTTP recorder, answered by github.com/arandu-io/hesape/log.

package observability

import (
	"net/http"
	"time"

	hlog "github.com/arandu-io/hesape/log"
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
	return hlog.Transport(next)
}

// Client returns an http.Client that records what it calls.
//
// The timeout is required rather than optional: http.Client has none by
// default, and a call with no deadline is how one slow dependency turns into
// every request of the process hanging.
func Client(timeout time.Duration) *http.Client {
	return hlog.Client(timeout)
}
