package jwks

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
)

// jwk is one entry of the JWKS "keys" array, per RFC 7517. Only the fields
// an RS256 verifier actually needs are included.
type jwk struct {
	Kty string `json:"kty"`
	Use string `json:"use"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type jwksDoc struct {
	Keys []jwk `json:"keys"`
}

// Handler serves the public half of kp as a JWKS document at whatever path
// it's mounted on (conventionally /.well-known/jwks.json). Only ever the
// public key leaves the process this way — kp.Private never does.
func Handler(kp *KeyPair) http.HandlerFunc {
	doc := jwksDoc{
		Keys: []jwk{
			{
				Kty: "RSA",
				Use: "sig",
				Kid: kp.KID,
				Alg: "RS256",
				N:   base64.RawURLEncoding.EncodeToString(kp.Private.N.Bytes()),
				E:   base64.RawURLEncoding.EncodeToString(bigEndianBytes(kp.Private.E)),
			},
		},
	}

	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=300")
		_ = json.NewEncoder(w).Encode(doc)
	}
}

// bigEndianBytes encodes the RSA public exponent (a small int, typically
// 65537) as minimal big-endian bytes, which is what the JWK "e" field
// expects.
func bigEndianBytes(e int) []byte {
	if e == 0 {
		return []byte{0}
	}
	var b []byte
	for e > 0 {
		b = append([]byte{byte(e & 0xff)}, b...)
		e >>= 8
	}
	return b
}
