// Package jwks provides both sides of RS256 key distribution: (a) a
// KeyPair auth-service signs tokens with and serves publicly (minus the
// private half) at /.well-known/jwks.json, and (b) a Client any other
// service (api-gateway, in Phase 1a) uses to fetch and cache those public
// keys by `kid`. Asymmetric signing + a published JWKS, instead of a
// shared HMAC secret in env vars everywhere, is what makes key rotation a
// config change on one service instead of a coordinated secret rollout
// across every service that verifies tokens. See docs/DECISIONS.md.
package jwks

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
)

// KeyPair bundles an RSA private key with the `kid` (key ID) that
// identifies its public half in JWKS output and in every token's header.
type KeyPair struct {
	Private *rsa.PrivateKey
	KID     string
}

// keyBits is 2048 — the RS256 minimum most verifiers expect and plenty for
// an access token with a short lifetime; there's no reason to pay 4096's
// CPU cost here.
const keyBits = 2048

// GenerateKeyPair creates a fresh RS256 keypair with a kid derived from a
// SHA-256 hash of the public key's modulus, so the kid is stable for the
// lifetime of a given key without needing separate storage for it.
func GenerateKeyPair() (*KeyPair, error) {
	priv, err := rsa.GenerateKey(rand.Reader, keyBits)
	if err != nil {
		return nil, fmt.Errorf("jwks: generate key: %w", err)
	}
	return &KeyPair{
		Private: priv,
		KID:     kidFor(&priv.PublicKey),
	}, nil
}

// LoadOrGenerate reads a PEM-encoded RSA private key from the
// JWT_PRIVATE_KEY_PEM environment variable if set, otherwise generates a
// fresh keypair. Generating on the fly is a deliberate dev convenience —
// see docs/DECISIONS.md — production deployments should set
// JWT_PRIVATE_KEY_PEM explicitly so restarts don't invalidate every
// outstanding token by rotating the key underneath them.
func LoadOrGenerate(getenv func(string) string) (*KeyPair, error) {
	pemStr := getenv("JWT_PRIVATE_KEY_PEM")
	if pemStr == "" {
		return GenerateKeyPair()
	}

	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("jwks: JWT_PRIVATE_KEY_PEM is set but not valid PEM")
	}

	var priv *rsa.PrivateKey
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		priv = key
	} else if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("jwks: JWT_PRIVATE_KEY_PEM is not an RSA key")
		}
		priv = rsaKey
	} else {
		return nil, fmt.Errorf("jwks: failed to parse JWT_PRIVATE_KEY_PEM: %w", err)
	}

	kid := getenv("JWT_KID")
	if kid == "" {
		kid = kidFor(&priv.PublicKey)
	}
	return &KeyPair{Private: priv, KID: kid}, nil
}

func kidFor(pub *rsa.PublicKey) string {
	sum := sha256.Sum256(pub.N.Bytes())
	return base64.RawURLEncoding.EncodeToString(sum[:8])
}
