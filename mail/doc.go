// Package mail sends what an application has to say to somebody.
//
// A Mailable declares an Envelope and a Content, a Mailer sends it, and the
// transport behind the Mailer is configuration rather than a decision the
// calling code makes.
//
// This package is a bridge. It is removed in v1.0.0; import github.com/arandu-io/hesape/mail directly.
//
// The components moved to github.com/arandu-io/hesape, under new names, and
// this package is now the old names pointing at them. It answers to two hesape
// packages:
//
//	hesape/mail            the vocabulary and the Mailer: Mailable, Envelope,
//	                       Content, Message, Render, ErrNoRecipient, ErrRetryable
//	hesape/mail/transport  the five transports that shipped here: SMTP, Log,
//	                       Array, Resend, SendGrid
//
// The death date above is what keeps this from being a second way to import one
// type. Nothing here holds an implementation: no message is rendered, no address
// is parsed and no byte is written by a line in this package. Where a name
// survived the move it is a Go alias, and where the design diverged it is an
// envelope that translates and nothing more.
//
// # The envelopes, and what diverged
//
// The split runs deeper here than in most of the collection, because the type at
// the bottom changed shape: hesape spells a mailbox Address{Address, Name}, and
// this package has always spelled it Address{Email, Name}. Every type that
// carries an address therefore had to stay declared here rather than become an
// alias -- Address, Envelope, Message -- and with them the two interfaces that
// mention those types, Mailable and Transport.
//
//	Address     Email is Address there
//	Envelope    carries this package's Address, and has no Using field
//	Content     TextView is Text there, which is a view name on both sides, and
//	            Data is With; the literal Text has no counterpart and is applied
//	            to the built message instead
//	Message     carries this package's Envelope
//	Transport   Send answers an error where hesape answers (SentMessage, error)
//	Mailable    answers this package's Envelope and Content
//	Mailer      hesape's takes a name and an event dispatcher, and its To takes
//	            one polymorphic argument where this one takes strings
//	Pending     PendingMail there, with a whole fluent surface
//	Array       Sent and Reset are Messages and Flush there
//
// # Three differences a caller can observe
//
// A Content whose Data is nil renders against the mailable rather than against
// nil: hesape's BuildViewData falls back to the mailable when no view data was
// named, and the value it falls back to is this package's adapter. A Content
// that names a View has always named its Data too, so this is the degenerate
// case rather than a path anything takes.
//
// Recipients are deduplicated, keeping the last spelling of a repeated address,
// which is where a display name comes from. This package used to send the same
// address twice.
//
// The recipients named on the call arrive before the ones named on the envelope,
// where they used to arrive after. Nothing reads that order except a transport
// writing a header.
package mail
