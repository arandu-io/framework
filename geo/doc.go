// Package geo bridges the native GEO component into the current Framework
// router envelope.
//
// All discovery behavior, configuration, metadata and HTTP handlers live in
// github.com/arandu-io/hesape/geo. This package translates only the Routes
// method because framework/http.Router is still an envelope over
// hesape/routing.Router. The bridge disappears with the other Framework
// compatibility packages when that envelope is removed.
package geo
