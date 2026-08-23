package jwks

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"
)

// Client fetches a JWKS document over HTTP and caches its public keys by
// `kid`, so a verifier (api-gateway) doesn't refetch on every single
// incoming request. A cached entry is reused until refreshInterval elapses,
// at which point the next lookup triggers a fresh fetch — this is what
// makes key rotation on auth-service's side eventually consistent within
// bounded time instead of requiring a gateway restart.
type Client struct {
	url             string
	refreshInterval time.Duration
	httpClient      *http.Client

	mu        sync.RWMutex
	keys      map[string]*rsaPublicKeyInfo
	fetchedAt time.Time
}

type rsaPublicKeyInfo struct {
	N *big.Int
	E int
}

// DefaultRefreshInterval balances "rotation takes effect reasonably
// quickly" against "don't hammer the JWKS endpoint on every request."
const DefaultRefreshInterval = 5 * time.Minute

// NewClient returns a Client for the JWKS document at url. Pass 0 for
// refreshInterval to use DefaultRefreshInterval.
func NewClient(url string, refreshInterval time.Duration) *Client {
	if refreshInterval <= 0 {
		refreshInterval = DefaultRefreshInterval
	}
	return &Client{
		url:             url,
		refreshInterval: refreshInterval,
		httpClient:      &http.Client{Timeout: 5 * time.Second},
		keys:            make(map[string]*rsaPublicKeyInfo),
	}
}

// Refresh fetches the JWKS document unconditionally and replaces the cache.
// Callers normally don't need to call this directly — GetKey refreshes
// lazily — but main() calling it once at startup (per Phase 1a's
// api-gateway) proves the plumbing works before the first real request
// needs a key.
func (c *Client) Refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return fmt.Errorf("jwks client: build request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("jwks client: fetch %s: %w", c.url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("jwks client: fetch %s: unexpected status %d", c.url, resp.StatusCode)
	}

	var doc jwksDoc
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return fmt.Errorf("jwks client: decode JWKS: %w", err)
	}

	keys := make(map[string]*rsaPublicKeyInfo, len(doc.Keys))
	for _, k := range doc.Keys {
		nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			continue
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			continue
		}
		e := 0
		for _, b := range eBytes {
			e = e<<8 | int(b)
		}
		keys[k.Kid] = &rsaPublicKeyInfo{N: new(big.Int).SetBytes(nBytes), E: e}
	}

	c.mu.Lock()
	c.keys = keys
	c.fetchedAt = time.Now()
	c.mu.Unlock()
	return nil
}

// GetKey returns the RSA public key (as N/E) for kid, refreshing the cache
// first if it's empty or stale. Returns an error if kid isn't found even
// after a refresh — the caller (typically a jwt.Keyfunc) should treat that
// as an invalid/unverifiable token.
func (c *Client) GetKey(ctx context.Context, kid string) (n *big.Int, e int, err error) {
	c.mu.RLock()
	stale := time.Since(c.fetchedAt) > c.refreshInterval || len(c.keys) == 0
	info, ok := c.keys[kid]
	c.mu.RUnlock()

	if !ok || stale {
		if refreshErr := c.Refresh(ctx); refreshErr != nil {
			if ok {
				// Serve the stale-but-present key rather than fail a
				// verification outright just because a refresh attempt
				// failed (e.g. transient network blip).
				return info.N, info.E, nil
			}
			return nil, 0, refreshErr
		}
		c.mu.RLock()
		info, ok = c.keys[kid]
		c.mu.RUnlock()
	}

	if !ok {
		return nil, 0, fmt.Errorf("jwks client: no key found for kid %q", kid)
	}
	return info.N, info.E, nil
}

// rsaPublicKeyFromParts rebuilds an *rsa.PublicKey from the N/E values
// GetKey returns, which is the shape a jwt.Keyfunc needs to hand back to
// the parser.
func rsaPublicKeyFromParts(n *big.Int, e int) *rsa.PublicKey {
	return &rsa.PublicKey{N: n, E: e}
}
