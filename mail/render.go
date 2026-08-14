// The wire format, answered by github.com/arandu-io/hesape/mail.

package mail

import hmail "github.com/arandu-io/hesape/mail"

// Render turns a message into the bytes an SMTP server receives.
//
// It is exported because a transport that speaks a provider's HTTP API does not
// need it and a transport that speaks SMTP does, and both live outside this
// package once the adapters exist.
//
// A wrapper and not an alias: a plain function has no alias form, and the
// argument is this package's Message rather than hesape's.
func Render(m Message) string { return hmail.Render(m.hesape()) }
