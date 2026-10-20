package foundation

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"github.com/arandu-io/framework/foundation/bootstrap"
	"github.com/arandu-io/hesape/cache"
)

// respEndpoint is where the RESP server is and how to reach it, as the
// connectors take it. One translation of REDIS_URL, read by the cache, the
// sessions and the queue alike, so the three cannot describe the server
// differently.
func respEndpoint(cfg bootstrap.Cache) (cache.Endpoint, error) {
	encryption, err := cacheTLS(cfg)
	if err != nil {
		return cache.Endpoint{}, err
	}
	return cache.Endpoint{
		Address:  cfg.Address,
		Password: cfg.Password,
		Database: cfg.Database,
		Prefix:   cfg.Prefix,
		TLS:      encryption,
	}, nil
}

// cacheTLS turns the file paths the configuration carries into the settings the
// connection takes, and answers nil when the URL asked for no encryption.
//
// A file that is named and cannot be read stops the boot, naming which one: the
// alternative is a process that starts with encryption off after being told to
// turn it on, and from the outside that looks like nothing at all.
func cacheTLS(cfg bootstrap.Cache) (*tls.Config, error) {
	named := cfg.TLSCAFile != "" || cfg.TLSCertFile != "" || cfg.TLSKeyFile != "" || cfg.TLSServerName != ""

	if !cfg.TLS {
		if named {
			// Refused rather than ignored: certificates configured for a
			// connection that carries none is somebody who believes the traffic
			// is encrypted and is wrong.
			return nil, fmt.Errorf("REDIS_URL asks for no encryption and the REDIS_*_FILE variables name certificates for it: " +
				"write the endpoint as rediss:// to turn it on, or remove them")
		}
		return nil, nil
	}

	// TLS 1.2 is the floor: the connection carries the password and every
	// session id.
	out := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: cfg.TLSServerName}

	if cfg.TLSCAFile != "" {
		authority, err := os.ReadFile(cfg.TLSCAFile)
		if err != nil {
			return nil, fmt.Errorf("REDIS_CA_FILE names %s, and the connection cannot be encrypted without it: %w", cfg.TLSCAFile, err)
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(authority) {
			return nil, fmt.Errorf("REDIS_CA_FILE names %s, and it holds no certificate this can read: it has to be PEM", cfg.TLSCAFile)
		}
		// The private root replaces the system pool rather than joining it, so
		// a certificate from either side cannot pass a check the operator meant
		// to narrow.
		out.RootCAs = roots
	}

	if (cfg.TLSCertFile == "") != (cfg.TLSKeyFile == "") {
		return nil, fmt.Errorf("REDIS_CERT_FILE and REDIS_KEY_FILE are a pair and only one is set: " +
			"a certificate without its key proves nothing, and a key without its certificate is sent to nobody")
	}
	if cfg.TLSCertFile != "" {
		pair, err := tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile)
		if err != nil {
			return nil, fmt.Errorf("REDIS_CERT_FILE names %s and REDIS_KEY_FILE names %s, and they are not a usable pair: %w",
				cfg.TLSCertFile, cfg.TLSKeyFile, err)
		}
		out.Certificates = []tls.Certificate{pair}
	}

	return out, nil
}
