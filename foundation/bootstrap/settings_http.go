package bootstrap

import (
	"fmt"
	"net/netip"
	"strings"
)

// DefaultMaxBodyBytes is the request body limit when HTTP_MAX_BODY_BYTES is not
// written: four mebibytes. It is sized for forms with room for a small
// attachment, not for uploads.
const DefaultMaxBodyBytes int64 = 4 << 20

// HTTP is how the server treats a request before any handler sees it: how much
// of a body it reads, and which peers may say where a request came from.
//
// It is the request boundary, beside HTTPServer, which is the listener's
// transport.
type HTTP struct {
	// MaxBodyBytes is the largest request body the application reads:
	// HTTP_MAX_BODY_BYTES.
	//
	// It is a ceiling for every route. A request that declares a larger body is
	// answered 413 before the session, the CSRF check or the handler does any
	// work, and a body that does not declare its length is cut off at the limit.
	// An upload route that takes more needs this raised; the form routes can
	// keep a tighter bound of their own on their group.
	MaxBodyBytes int64

	// TrustedProxies are the peers whose X-Forwarded-For and X-Forwarded-Proto
	// are believed: TRUSTED_PROXIES, a comma-separated list of addresses or
	// CIDR prefixes.
	//
	// Empty is the default and means nobody is: a process listening on the
	// internet directly must not let a client name its own address, because the
	// sign-in throttle and every rate limit key on it.
	TrustedProxies []netip.Prefix
}

func loadHTTPSettings() (HTTP, error) {
	limit, err := envBytes("HTTP_MAX_BODY_BYTES", DefaultMaxBodyBytes)
	if err != nil {
		return HTTP{}, err
	}
	proxies, err := parseTrustedProxies(env("TRUSTED_PROXIES", ""))
	if err != nil {
		return HTTP{}, err
	}
	return HTTP{MaxBodyBytes: limit, TrustedProxies: proxies}, nil
}

// parseTrustedProxies reads a comma-separated list of CIDR prefixes. A bare
// address is accepted and trusted alone, as a /32 or a /128.
//
// A wildcard is refused: trusting every peer is trusting every client to say
// who it is. A deployment that truly sits behind a proxy on every address
// writes 0.0.0.0/0,::/0 and owns that sentence.
func parseTrustedProxies(raw string) ([]netip.Prefix, error) {
	var prefixes []netip.Prefix
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if part == "*" || part == "**" {
			return nil, fmt.Errorf("TRUSTED_PROXIES does not take %q: list the proxies' addresses or CIDR prefixes, because trusting every peer lets any client choose the address it is throttled under", part)
		}
		if prefix, err := netip.ParsePrefix(part); err == nil {
			prefixes = append(prefixes, prefix.Masked())
			continue
		}
		addr, err := netip.ParseAddr(part)
		if err != nil {
			return nil, fmt.Errorf("TRUSTED_PROXIES has invalid entry %q: want an address or a CIDR prefix such as 10.0.0.0/8", part)
		}
		addr = addr.Unmap()
		prefixes = append(prefixes, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return prefixes, nil
}
