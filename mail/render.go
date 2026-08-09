package mail

import (
	"fmt"
	"mime"
	"mime/quotedprintable"
	"strings"
	"time"
)

// Render turns a message into the bytes an SMTP server receives.
//
// It is exported because a transport that speaks a provider's HTTP API does not
// need it and a transport that speaks SMTP does, and both live outside this
// package once the adapters exist.
//
// # What it gets right that a Sprintf does not
//
// A header carrying a non-ASCII subject has to be encoded, or the client shows
// mojibake -- and "Você tem uma fatura" is the first subject anybody writes here.
// mime.QEncoding does it, and does nothing when the text is plain ASCII.
//
// A body line longer than 998 bytes is refused by the protocol, and an HTML
// document is one long line often enough. quoted-printable folds it.
//
// A line consisting of a single dot ends the message: a body containing one is
// a body that is silently truncated, and the transfer encoding removes that too.
func Render(m Message) string {
	var b strings.Builder

	header(&b, "From", m.From.String())
	header(&b, "To", addresses(m.To))
	if len(m.CC) > 0 {
		header(&b, "Cc", addresses(m.CC))
	}
	if len(m.ReplyTo) > 0 {
		header(&b, "Reply-To", addresses(m.ReplyTo))
	}
	// Bcc is deliberately not a header. It is on the envelope, in RCPT TO, and
	// writing it here is how every recipient learns who else was blind-copied.

	header(&b, "Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	header(&b, "Date", time.Now().Format(time.RFC1123Z))
	header(&b, "MIME-Version", "1.0")

	switch {
	case m.HTML != "" && m.Text != "":
		// Both parts, in the order the standard asks for: least preferred first,
		// so a client that renders HTML picks the last one it understands.
		boundary := "arandu-" + fmt.Sprintf("%d", time.Now().UnixNano())
		header(&b, "Content-Type", `multipart/alternative; boundary="`+boundary+`"`)
		b.WriteString("\r\n")

		part(&b, boundary, "text/plain; charset=utf-8", m.Text)
		part(&b, boundary, "text/html; charset=utf-8", m.HTML)
		b.WriteString("--" + boundary + "--\r\n")

	case m.HTML != "":
		header(&b, "Content-Type", "text/html; charset=utf-8")
		header(&b, "Content-Transfer-Encoding", "quoted-printable")
		b.WriteString("\r\n")
		b.WriteString(encode(m.HTML))

	default:
		header(&b, "Content-Type", "text/plain; charset=utf-8")
		header(&b, "Content-Transfer-Encoding", "quoted-printable")
		b.WriteString("\r\n")
		b.WriteString(encode(m.Text))
	}

	return b.String()
}

func header(b *strings.Builder, name, value string) {
	b.WriteString(name)
	b.WriteString(": ")
	b.WriteString(value)
	b.WriteString("\r\n")
}

func part(b *strings.Builder, boundary, contentType, body string) {
	b.WriteString("--" + boundary + "\r\n")
	header(b, "Content-Type", contentType)
	header(b, "Content-Transfer-Encoding", "quoted-printable")
	b.WriteString("\r\n")
	b.WriteString(encode(body))
	b.WriteString("\r\n")
}

func encode(body string) string {
	var out strings.Builder
	w := quotedprintable.NewWriter(&out)
	_, _ = w.Write([]byte(body))
	_ = w.Close()
	return out.String()
}

func addresses(list []Address) string {
	out := make([]string, 0, len(list))
	for _, a := range list {
		out = append(out, a.String())
	}
	return strings.Join(out, ", ")
}
