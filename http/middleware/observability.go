// The request observer, answered by github.com/arandu-io/hesape/foundation.

package middleware

import (
	"net/http"

	"github.com/arandu-io/framework/observability"
	hfoundation "github.com/arandu-io/hesape/foundation"
)

// Observe installs the request id, the request-scoped logger and -- in
// development, or under an authorized tracing header -- the Collector.
//
// It must come right after Recover: everything below depends on the context it
// builds.
//
// tracingSecret enables the Collector outside development for requests carrying
// it in X-Arandu-Trace. Leave it empty to keep production at zero cost.
//
// recorder is the buffer behind /_arandu/debug. Pass kernel.Recorder(); nil
// records nothing, which is what production does.
//
// It is hesape/foundation.Observe under its old name.
func Observe(dev bool, tracingSecret string, recorder *observability.Recorder) func(http.Handler) http.Handler {
	return hfoundation.Observe(dev, tracingSecret, recorder)
}
