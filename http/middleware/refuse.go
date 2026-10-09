package middleware

import (
	"net/http"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/hesape/exception"
	hhttp "github.com/arandu-io/hesape/http"
)

// refuse answers a request a middleware turns away, in the shape the client
// asked for.
//
// A client that wants JSON -- by Accept or X-Requested-With, the rule
// Context.WantsJSON states and htmx is excluded from -- gets a problem document
// through exception.WriteProblem, the one writer of that shape. Anything else
// gets fhttp.Refuse, the answer the session guards give. Neither is to be kept
// by a cache shared between people, because a refusal is one caller's.
func refuse(w http.ResponseWriter, r *http.Request, status int, detail string) {
	if hhttp.NewContext(w, r, nil, nil).WantsJSON() {
		exception.WriteProblem(w, r, status, detail)
		return
	}
	w.Header().Set("Cache-Control", "no-store, private")
	fhttp.Refuse(w, r, status, detail)
}
