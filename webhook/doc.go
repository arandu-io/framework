// Package webhook registers the native webhook component on the Framework
// router envelope.
//
// Everything the component does -- its configuration, its tables, its
// policies, its receivers, its deliveries and every HTTP action -- lives in
// github.com/arandu-io/hesape/webhook. This package translates only the
// registration, because framework/http.Router is still an envelope over
// hesape/routing.Router: it carries the renderer and the flash, and it answers
// an error a controller action returns with its own adapter. The native module
// is built with hesape's New and handed to NewModule; nothing here declares a
// second name for any of its types. The package disappears with the other
// Framework compatibility packages when that envelope is removed.
package webhook
