package jwks

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TestSignAndVerify_RoundTrip exercises the full loop this package exists
// for: generate a keypair, sign a token, serve it as JWKS over HTTP, fetch
// it back through Client, and verify the token using only the
// JWKS-derived public key (never the original KeyPair) — proving a
// verifier that only ever sees the published JWKS (api-gateway, in a real
// deployment) can actually validate tokens auth-service issues.
func TestSignAndVerify_RoundTrip(t *testing.T) {
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	tokenStr, err := Sign(kp, "test-issuer", "user-123", "user@example.com", "user", time.Minute)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	srv := httptest.NewServer(Handler(kp))
	defer srv.Close()

	client := NewClient(srv.URL, time.Minute)

	parsed, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		kid, _ := token.Header["kid"].(string)
		n, e, err := client.GetKey(t.Context(), kid)
		if err != nil {
			return nil, err
		}
		return rsaPublicKeyFromParts(n, e), nil
	})
	if err != nil {
		t.Fatalf("ParseWithClaims: %v", err)
	}
	if !parsed.Valid {
		t.Fatal("expected parsed token to be valid")
	}

	claims, ok := parsed.Claims.(*Claims)
	if !ok {
		t.Fatal("expected *Claims")
	}
	if claims.Subject != "user-123" {
		t.Errorf("expected subject user-123, got %q", claims.Subject)
	}
	if claims.Email != "user@example.com" {
		t.Errorf("expected email user@example.com, got %q", claims.Email)
	}
	if claims.Issuer != "test-issuer" {
		t.Errorf("expected issuer test-issuer, got %q", claims.Issuer)
	}
}

func TestClient_UnknownKidErrors(t *testing.T) {
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	srv := httptest.NewServer(Handler(kp))
	defer srv.Close()

	client := NewClient(srv.URL, time.Minute)
	if _, _, err := client.GetKey(t.Context(), "does-not-exist"); err == nil {
		t.Fatal("expected error for unknown kid")
	}
}
