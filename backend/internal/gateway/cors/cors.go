// Package cors implements api-gateway's CORS policy — the gap
// docs/SECURITY.md tracked as "no CORS configuration exists yet" and the
// plan's Phase 1a bullet flagged as needed "once frontend integration
// starts." Phase 7 is the first point a real deployed frontend origin
// exists as a concept (a Vercel URL, even before the exact one is known),
// so this is where it's finally wired in — see docs/DECISIONS.md's Phase 7
// notes.
package cors

import (
	"net/http"
	"strings"

	"go.uber.org/zap"
)

// DefaultAllowedOrigins is what CORS_ALLOWED_ORIGINS falls back to when
// unset — a bare local `npm run dev` (Vite's default port). Deliberately
// not "*": this project's auth is a bearer token in an Authorization
// header, not a cookie, so a wildcard origin wouldn't itself leak a
// session the way it could for cookie-based auth — but defaulting to "*"
// still normalizes "any origin may call this API" as this middleware's
// baseline behavior, which is the wrong default to carry forward into
// whatever CORS_ALLOWED_ORIGINS eventually gets set to for a real
// deployment. An explicit, small allowlist is the point of this package.
var DefaultAllowedOrigins = []string{"http://localhost:5173"}

// LoadAllowedOriginsFromEnv parses CORS_ALLOWED_ORIGINS (comma-separated,
// each entry whitespace-trimmed, e.g.
// "https://skill-bridge.vercel.app,http://localhost:5173") via getenv, or
// returns DefaultAllowedOrigins if it's unset/empty/all-whitespace.
func LoadAllowedOriginsFromEnv(getenv func(string) string) []string {
	raw := getenv("CORS_ALLOWED_ORIGINS")
	if strings.TrimSpace(raw) == "" {
		return DefaultAllowedOrigins
	}

	parts := strings.Split(raw, ",")
	origins := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			origins = append(origins, p)
		}
	}
	if len(origins) == 0 {
		return DefaultAllowedOrigins
	}
	return origins
}

// Middleware returns HTTP middleware enforcing allowedOrigins as an exact
// allowlist: a request's Origin header must match one entry verbatim
// (scheme + host + port, no wildcard/subdomain matching) — a small, fixed
// set of known frontend origins (local dev, eventually one Vercel URL) is
// exactly this project's shape, so there's no need for anything fancier.
//
// Only ever echoes back the *matching* origin in
// Access-Control-Allow-Origin, never "*" — required anyway once
// credentials are ever involved, and the more honest response even
// without them: "this exact origin is allowed," not "every origin is."
// Always sets Vary: Origin so a cache sitting in front of this (a CDN, a
// browser's own HTTP cache) can't serve one origin's response to another.
//
// A non-matching or missing Origin gets no CORS headers at all — the
// browser's own same-origin-policy enforcement then blocks the response
// from being read by the calling page's JS, which is the actual
// enforcement point; this middleware never needs to reject the request
// itself (a same-origin browser navigation, curl, a server-to-server
// call, and grpcurl-style debugging all have no Origin header and must
// keep working exactly as before).
func Middleware(allowedOrigins []string, log *zap.Logger) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allowed[o] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Add("Vary", "Origin")

			origin := r.Header.Get("Origin")
			if origin != "" {
				if _, ok := allowed[origin]; ok {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
					w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
				} else if log != nil {
					log.Debug("CORS: origin not in allowlist, no CORS headers set", zap.String("origin", origin))
				}
			}

			if r.Method == http.MethodOptions {
				// A CORS preflight terminates here regardless of match —
				// the response above (with or without
				// Access-Control-Allow-Origin set) is the entire answer;
				// there's no GraphQL/health-check meaning to an OPTIONS
				// request for next to handle.
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
