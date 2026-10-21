package unit

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/framework/mail"
)

// selfSigned is a certificate for 127.0.0.1, and the pool that trusts it.
func selfSigned(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "smtp.test"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, pool
}

// smtpServer answers one SMTP session on l and hands back the DATA it was
// sent, or the empty string when the session ended before DATA.
func smtpServer(t *testing.T, l net.Listener) <-chan string {
	t.Helper()
	got := make(chan string, 1)
	go func() {
		defer close(got)
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		r := bufio.NewReader(conn)
		write := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
		write("220 smtp.test ESMTP")
		var data strings.Builder
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				got <- data.String()
				return
			}
			switch cmd := strings.ToUpper(strings.TrimSpace(line)); {
			case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
				write("250 smtp.test")
			case strings.HasPrefix(cmd, "MAIL"), strings.HasPrefix(cmd, "RCPT"):
				write("250 ok")
			case cmd == "DATA":
				write("354 go ahead")
				for {
					l, err := r.ReadString('\n')
					if err != nil || l == ".\r\n" {
						break
					}
					data.WriteString(l)
				}
				write("250 queued")
			case cmd == "QUIT":
				write("221 bye")
				got <- data.String()
				return
			default:
				write("502 not implemented")
			}
		}
	}()
	return got
}

func message() mail.Message {
	return mail.Message{
		Envelope: mail.Envelope{
			From:    mail.Address{Email: "app@example.test"},
			To:      []mail.Address{{Email: "ana@example.test"}},
			Subject: "Receipt",
		},
		Text: "Paid in full.",
	}
}

// TestSMTPSpeaksImplicitTLSWhenTheConfigurationSaysSo: with Encryption "tls"
// -- what smtps:// sets -- the transport completes a TLS handshake before the
// server's greeting, against the authority it was given, and the message
// arrives over it.
func TestSMTPSpeaksImplicitTLSWhenTheConfigurationSaysSo(t *testing.T) {
	cert, pool := selfSigned(t)
	l, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	got := smtpServer(t, l)

	host, port, _ := net.SplitHostPort(l.Addr().String())
	tr := mail.SMTP{Host: host, Port: port, Encryption: "tls", Timeout: 5 * time.Second,
		TLSConfig: &tls.Config{RootCAs: pool}}
	if err := tr.Send(context.Background(), message()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if body := <-got; !strings.Contains(body, "Paid in full.") {
		t.Fatalf("the server received %q, want the message", body)
	}
}

// TestSMTPRefusesAServerItCannotVerify: implicit TLS against a certificate
// nobody vouched for sends nothing.
func TestSMTPRefusesAServerItCannotVerify(t *testing.T) {
	cert, _ := selfSigned(t)
	l, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	got := smtpServer(t, l)

	host, port, _ := net.SplitHostPort(l.Addr().String())
	tr := mail.SMTP{Host: host, Port: port, Encryption: "tls", Timeout: 5 * time.Second}
	if err := tr.Send(context.Background(), message()); err == nil || !strings.Contains(err.Error(), "tls") {
		t.Fatalf("Send = %v, want the handshake refused", err)
	}
	l.Close()
	if body := <-got; body != "" {
		t.Fatalf("the server received %q from a client that could not verify it", body)
	}
}

// TestSMTPWithImplicitTLSSendsNothingToAPlainServer: a server that speaks
// SMTP in the clear is never greeted, because the handshake comes first.
func TestSMTPWithImplicitTLSSendsNothingToAPlainServer(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	got := smtpServer(t, l)

	host, port, _ := net.SplitHostPort(l.Addr().String())
	tr := mail.SMTP{Host: host, Port: port, Encryption: "tls", Timeout: 2 * time.Second}
	if err := tr.Send(context.Background(), message()); err == nil {
		t.Fatal("Send succeeded over a connection that was never encrypted")
	}
	l.Close()
	if body := <-got; strings.Contains(body, "Paid in full.") {
		t.Fatal("the message went out in the clear")
	}
}

// TestSMTPRefusesAnEncryptionItDoesNotKnow: a misspelling is an error, never
// a plain session.
func TestSMTPRefusesAnEncryptionItDoesNotKnow(t *testing.T) {
	err := mail.SMTP{Host: "127.0.0.1", Port: "1", Encryption: "tsl"}.Send(context.Background(), message())
	if err == nil || !strings.Contains(err.Error(), `"tsl"`) {
		t.Fatalf("Send = %v, want the unknown encryption named", err)
	}
}

// TestSMTPInTheClearStillSendsWithoutEncryption: the empty value keeps what
// the transport always did against a relay that offers no STARTTLS.
func TestSMTPInTheClearStillSendsWithoutEncryption(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	got := smtpServer(t, l)

	host, port, _ := net.SplitHostPort(l.Addr().String())
	if err := (mail.SMTP{Host: host, Port: port, Timeout: 5 * time.Second}).Send(context.Background(), message()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if body := <-got; !strings.Contains(body, "Paid in full.") {
		t.Fatalf("the server received %q, want the message", body)
	}
}
