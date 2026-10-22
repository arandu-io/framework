package bootstrap

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Mailer is the transport outgoing mail leaves by.
type Mailer string

// The supported mailers.
const (
	// MailerLog writes the message to the log instead of sending it. The
	// default: an application that sends real mail from a laptop is one that
	// mails a customer during a test. Production refuses it, because the log
	// then holds every message body, password-reset codes included.
	MailerLog Mailer = "log"
	// MailerSMTP is any SMTP server.
	MailerSMTP Mailer = "smtp"
	// MailerResend is Resend, over one HTTPS call.
	MailerResend Mailer = "resend"
	// MailerSendGrid is SendGrid, over one HTTPS call.
	MailerSendGrid Mailer = "sendgrid"
	// MailerArray keeps messages in memory, for tests to read.
	MailerArray Mailer = "array"
)

// Mail is where outgoing messages go, and it comes from one URL.
//
//	MAIL_URL=log://                                   the default
//	MAIL_URL=smtp://user:password@smtp.example.com:587
//	MAIL_URL=smtps://user:password@smtp.example.com   implicit TLS on 465
//	MAIL_URL=resend://re_xxxxxxxx
//	MAIL_URL=sendgrid://SG.xxxxxxxx
//	MAIL_URL=array://                                 what tests read
//
// One URL rather than seven variables: the scheme is the transport, so a
// configuration cannot say resend and carry an SMTP host. The sender is not
// part of it -- MAIL_FROM_ADDRESS and MAIL_FROM_NAME are who the application
// is, and they do not change when the provider does.
type Mail struct {
	Mailer Mailer

	// Host, Port, Username and Password describe the SMTP server, parsed out of
	// the URL when the scheme is smtp or smtps.
	Host     string
	Port     int
	Username string
	Password string

	// Encryption is tls or starttls. There is no "none": an unencrypted SMTP
	// session sends the password in the clear.
	Encryption string

	// Key authenticates with Resend or SendGrid, and it is the host of the URL.
	Key string

	// FromAddress and FromName are the envelope every message leaves with.
	FromAddress string
	FromName    string
}

// retiredMailVars are the variables MAIL_URL replaced. Setting one is refused
// rather than ignored: a MAIL_KEY nobody reads is a provider silently not
// configured.
var retiredMailVars = []string{
	"MAIL_MAILER", "MAIL_HOST", "MAIL_PORT",
	"MAIL_USERNAME", "MAIL_PASSWORD", "MAIL_ENCRYPTION", "MAIL_KEY",
}

func loadMailSettings(base Configuration) (Mail, error) {
	for _, name := range retiredMailVars {
		if env(name, "") == "" {
			continue
		}
		return Mail{}, fmt.Errorf("%s is retired; remove it and configure the mailer with MAIL_URL", name)
	}

	cfg, err := parseMailURL(env("MAIL_URL", "log://"))
	if err != nil {
		return Mail{}, err
	}
	if cfg.Mailer == MailerLog && base.App.Env.IsProduction() {
		return Mail{}, fmt.Errorf("MAIL_URL=log:// is forbidden in production, and it is the value when MAIL_URL is unset: " +
			"the log transport writes every message, password-reset codes included, into the log; " +
			"set MAIL_URL to smtp://, smtps://, resend:// or sendgrid://")
	}
	cfg.FromAddress = env("MAIL_FROM_ADDRESS", "no-reply@localhost")
	cfg.FromName = env("MAIL_FROM_NAME", base.App.Name)
	return cfg, nil
}

// parseMailURL reads the one variable. A bad value is returned to the boot
// rather than falling back to the log transport, which would report a
// successful boot while messages go somewhere the deployment did not choose.
func parseMailURL(raw string) (Mail, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "log://"
	}

	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return Mail{}, fmt.Errorf("MAIL_URL has invalid URL value %q", raw)
	}

	cfg := Mail{Mailer: Mailer(strings.ToLower(u.Scheme))}

	switch cfg.Mailer {
	case MailerLog, MailerArray:
		// Complete in their scheme.

	case MailerSMTP, "smtps":
		cfg.Mailer = MailerSMTP
		cfg.Host = u.Hostname()
		cfg.Port = 587
		if rawPort := u.Port(); rawPort != "" {
			port, err := strconv.Atoi(rawPort)
			if err != nil || port <= 0 {
				return Mail{}, fmt.Errorf("MAIL_URL has invalid SMTP port %q", rawPort)
			}
			cfg.Port = port
		}
		if u.User != nil {
			cfg.Username = u.User.Username()
			cfg.Password, _ = u.User.Password()
		}
		// smtps is implicit TLS on 465; smtp is STARTTLS, which is what 587
		// means.
		cfg.Encryption = "starttls"
		if strings.EqualFold(u.Scheme, "smtps") {
			cfg.Encryption = "tls"
			if u.Port() == "" {
				cfg.Port = 465
			}
		}
		if cfg.Host == "" {
			return Mail{}, fmt.Errorf("MAIL_URL names no SMTP host")
		}

	case MailerResend, MailerSendGrid:
		cfg.Key = u.Host
		if cfg.Key == "" {
			return Mail{}, fmt.Errorf("MAIL_URL carries no API key for %s", u.Scheme)
		}

	default:
		return Mail{}, fmt.Errorf("MAIL_URL uses unsupported scheme %q; expected log, smtp, smtps, resend, sendgrid or array", u.Scheme)
	}

	return cfg, nil
}
