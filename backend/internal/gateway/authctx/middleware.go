package authctx

import (
	"net/http"
	"strings"

	"go.uber.org/zap"

	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/jwks"
)

// Middleware extracts an `Authorization: Bearer <token>` header, verifies
// it against auth-service's JWKS via jwksClient, and — only when it's
// present and valid — stores the token's `sub` claim in the request
// context via NewContext for downstream resolvers to read via UserID.
//
// A request with no Authorization header at all is passed through
// unchanged, not rejected here: skills/jobs queries and createSkill/
// createJob stay unauthenticated in Phase 1b (see docs/DECISIONS.md —
// there's no role system yet), and they share this same HTTP handler with
// the authenticated operations (myProfile/updateProfile/addUserSkill),
// since GraphQL serves every operation through one endpoint. A malformed
// or invalid token is also not rejected at this layer with an HTTP error —
// it's logged and simply treated as "no verified caller," so an
// unauthenticated query sent alongside a stale token doesn't fail for an
// unrelated reason. The actual authorization decision ("does this
// operation require a caller, and is one present") belongs to each
// resolver that needs it (see schema.resolvers.go's requireUserID),
// returning a normal GraphQL error rather than an HTTP-layer rejection —
// keeping every GraphQL response on the same transport-level contract
// regardless of which operation failed and why.
func Middleware(jwksClient *jwks.Client, log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			token, ok := strings.CutPrefix(header, "Bearer ")
			token = strings.TrimSpace(token)
			if !ok || token == "" {
				next.ServeHTTP(w, r)
				return
			}

			claims, err := jwks.Verify(r.Context(), jwksClient, token)
			if err != nil {
				log.Debug("ignoring invalid bearer token", zap.Error(err))
				next.ServeHTTP(w, r)
				return
			}
			if claims.Subject == "" {
				log.Debug("ignoring bearer token with no subject claim")
				next.ServeHTTP(w, r)
				return
			}

			next.ServeHTTP(w, r.WithContext(NewContext(r.Context(), claims.Subject)))
		})
	}
}
