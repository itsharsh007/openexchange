package middleware

import (
	"net/http"
	"strings"
)

// CORS adds cross-origin headers so the dashboard (a different origin) can call
// the gateway. `allowed` is the raw CORS_ALLOWED_ORIGIN value:
//
//   - "*"                          → reflect any origin (dev only; logged as a warning at boot)
//   - "https://app.example"        → allow exactly that origin
//   - "https://a.example,https://b" → allowlist; the request's Origin is reflected
//     only if it's in the list
//
// We reflect the matched Origin (rather than echoing "*") so the response is
// correct even when several origins are permitted, and so it stays compatible
// with credentialed requests if those are added later.
func CORS(allowed string) func(http.Handler) http.Handler {
	wildcard := strings.TrimSpace(allowed) == "*"
	list := make(map[string]bool)
	for _, o := range strings.Split(allowed, ",") {
		if o = strings.TrimSpace(o); o != "" {
			list[o] = true
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			switch {
			case wildcard:
				w.Header().Set("Access-Control-Allow-Origin", "*")
			case origin != "" && list[origin]:
				w.Header().Set("Access-Control-Allow-Origin", origin)
				// Caches must vary on Origin since the header is request-dependent.
				w.Header().Add("Vary", "Origin")
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")

			// Preflight — browser sends OPTIONS before POST/DELETE with custom headers.
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// SecurityHeaders sets conservative response headers on every request. These are
// cheap defense-in-depth: stop MIME sniffing, deny framing (clickjacking), and
// trim the Referer leaked cross-origin.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}
