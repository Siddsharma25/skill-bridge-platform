package jwks

import (
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims is the access-token payload every service in this project should
// treat as the standard shape. `sub` (via RegisteredClaims.Subject) carries
// the user ID; `email` is included so the gateway can log/attribute
// requests without a round trip back to auth-service. Role-based claims
// (user_role) are deferred until a later phase actually needs
// authorization decisions beyond "is this a valid token."
type Claims struct {
	Email string `json:"email"`
	jwt.RegisteredClaims
}

// DefaultTTL is how long an issued access token is valid. Short-lived on
// purpose — there is no refresh-token rotation yet (that's scoped to a
// later phase's cross-origin auth work per docs/DECISIONS.md), so this is
// the whole session lifetime for now.
const DefaultTTL = 1 * time.Hour

// Sign issues an RS256-signed access token for userID/email, using kp and
// stamping the `kid` in the token header so a verifier knows which JWKS
// entry to check it against.
func Sign(kp *KeyPair, issuer, userID, email string, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	now := time.Now()
	claims := Claims{
		Email: email,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			Issuer:    issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = kp.KID
	return token.SignedString(kp.Private)
}
