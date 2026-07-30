// This program must NOT compile.
//
// It is the second negative fixture of the data package: even knowing the shape
// of security.Grant, code outside that package cannot build a valid one, because
// every field is unexported. The zero value is the only Grant available, and it
// fails Check at runtime.
package main

import (
	"context"

	"github.com/arandu-io/framework/modules/auth"
	"github.com/arandu-io/framework/security"
)

func main() {
	var repo *auth.UserRepo

	// Forging a valid Grant: the fields are unexported, so this does not compile.
	forged := security.Grant{valid: true, action: auth.ActionUserView}

	_, _ = repo.Find(context.Background(), forged, "some-id")
}
