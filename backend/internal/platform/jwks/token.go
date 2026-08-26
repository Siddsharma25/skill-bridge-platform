package jwks

import (
	"context"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims is the access-token payload every service in this project should
// treat as the standard shape. `sub` (via RegisteredClaims.Subject) carries
// the user ID; `email` is included so the gateway can log/attribute
// requests without a round trip back to auth-service. `role` (RBAC) is
// included so the gateway can make an authorization decision (e.g.
// createSkill's admin-only gate — see schema.resolvers.go and
// docs/DECISIONS.md's RBAC notes) from the verified token alone, without
// a round trip back to auth-service for every request.
type Claims struct {
	Email string `json:"email"`
	Role  string `json:"role"`
	jwt.RegisteredClaims
}

// DefaultTTL is how long an issued access token is valid. Short-lived on
// purpose — there is no refresh-token rotation yet (that's scoped to a
// later phase's cross-origin auth work per docs/DECISIONS.md), so this is
// the whole session lifetime for now.
const DefaultTTL = 1 * time.Hour

// Sign issues an RS256-signed access token for userID/email/role, using kp
// and stamping the `kid` in the token header so a verifier knows which
// JWKS entry to check it against.
func Sign(kp *KeyPair, issuer, userID, email, role string, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	now := time.Now()
	claims := Claims{
		Email: email,
		Role:  role,
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

// Verify is the verifier-side companion to Sign: it parses tokenString,
// looks up the RSA public key named by the token's `kid` header via
// client (fetching/caching from auth-service's JWKS as needed — see
// client.go), and returns the validated claims. This is what api-gateway
// uses to authenticate an incoming `Authorization: Bearer <token>` header
// against auth-service's published keys, without auth-service ever
// sharing its private key (see docs/DECISIONS.md's "gateway is the only
// JWT verifier" decision).
func Verify(ctx context.Context, client *Client, tokenString string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("jwks: unexpected signing method %v", t.Header["alg"])
		}
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, fmt.Errorf("jwks: token is missing a kid header")
		}
		n, e, err := client.GetKey(ctx, kid)
		if err != nil {
			return nil, err
		}
		return rsaPublicKeyFromParts(n, e), nil
	})
	if err != nil {
		return nil, fmt.Errorf("jwks: verify token: %w", err)
	}
	if !token.Valid {
		return nil, fmt.Errorf("jwks: token failed validation")
	}
	return claims, nil
}
