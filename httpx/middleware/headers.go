package middleware

import "net/http"

// SecurityHeaders applies the default headers.
//
// The CSP is restrictive on purpose and still works with HTMX, because HTMX
// operates through attributes rather than inline script. A module that truly
// needs inline code asks for an explicit nonce -- there is no global
// 'unsafe-inline' in this framework.
func SecurityHeaders(dev bool) func(http.Handler) http.Handler {
	csp := "default-src 'self'; " +
		"script-src 'self'; " +
		"style-src 'self'; " +
		// Explicit, though default-src already covers it. A font is vendored and
		// served from this origin like everything else (view.RegisterFont), and
		// spelling it out is what makes the line say so -- the next person
		// wondering whether a Google Fonts URL would work reads the policy, not
		// the fallback rules.
		"font-src 'self'; " +
		"img-src 'self' data:; " +
		"connect-src 'self'; " +
		"frame-ancestors 'none'; " +
		"base-uri 'self'; " +
		"form-action 'self'"

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			h.Set("Content-Security-Policy", csp)
			h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
			if !dev {
				// HSTS over plain HTTP would pin localhost to https and break
				// every developer's machine, so it is production only.
				h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}
