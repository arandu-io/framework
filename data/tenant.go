// The tenant, answered by github.com/arandu-io/hesape/auth.
//
// It is the one symbol in this package that did not go to hesape/database, and
// the move is the point: reading a tenant is reading one field off a Grant, and
// it lived in the SQL package, so the cache, the filesystem and the scheduler
// all had to import the database to prefix a key with the customer it belongs
// to.

package data

import "github.com/arandu-io/hesape/auth"

// Tenant returns the tenant from the Grant. Every multi-tenant statement must
// take this value, never a tenant that came in with the request (RULE 14).
//
// Renamed on the way to hesape only in address: it is auth.Tenant there, with
// the same signature and the same one line of body.
func Tenant(g auth.Grant) string { return auth.Tenant(g) }
