package auth

// Google OAuth (Phase 1c). See docs/DECISIONS.md for the account-linking
// rule this file implements and for why the token exchange (not the
// database) is the thing tests mock — GoogleExchanger is the seam real
// code implements against Google's actual endpoints and tests fake with
// canned responses, so oauth_test.go never makes a network call. There is
// no real Google Cloud OAuth client yet (external dependency, out of this
// repo's control) — GetGoogleAuthURL/GoogleOAuthCallback both degrade to a
// clear FailedPrecondition status when GOOGLE_OAUTH_CLIENT_ID/SECRET/
// REDIRECT_URL aren't configured, the same pattern as a missing
// DATABASE_URL elsewhere in this codebase.

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	authv1 "github.com/Siddsharma25/skill-bridge-platform/backend/gen/auth/v1"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/jwks"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/logger"
)

// googleUserInfoURL is Google's OpenID Connect userinfo endpoint. Calling
// it with the access token from the exchange gives a server-verified
// subject/email pair; the alternative (decoding the `id_token` JWT
// client-side without checking its signature) would mean trusting an
// unverified claim, which defeats the point of OAuth.
const googleUserInfoURL = "https://openidconnect.googleapis.com/v1/userinfo"

// GoogleExchanger abstracts the two things this file needs from Google's
// OAuth endpoints: building the consent URL, and exchanging an
// authorization code for a verified identity. Exported so
// cmd/auth-service/main.go can hold a value of this type; the concrete
// implementation (googleOAuth2Exchanger, below) wraps golang.org/x/oauth2
// and makes real HTTP calls, while oauth_test.go substitutes a fake that
// never does — see docs/DECISIONS.md for why the exchange specifically
// (not the DB layer) is what's mocked in tests.
type GoogleExchanger interface {
	AuthCodeURL(state string) string
	Exchange(ctx context.Context, code string) (*googleUserInfo, error)
}

// googleUserInfo is the subset of Google's userinfo response this codebase
// actually uses.
type googleUserInfo struct {
	Subject string
	Email   string
}

type googleOAuth2Exchanger struct {
	cfg        *oauth2.Config
	httpClient *http.Client
}

// NewGoogleExchangerFromEnv builds a GoogleExchanger from
// GOOGLE_OAUTH_CLIENT_ID/GOOGLE_OAUTH_CLIENT_SECRET/GOOGLE_OAUTH_REDIRECT_URL.
// ok is false if any of the three is unset, in which case the caller
// (cmd/auth-service/main.go) is expected to log a clear warning and start
// the service with Google OAuth disabled rather than fail to start — there
// is no live Google Cloud OAuth client yet, so this is the expected state
// for local dev and CI (see docs/DECISIONS.md).
func NewGoogleExchangerFromEnv(getenv func(string) string) (GoogleExchanger, bool) {
	clientID := getenv("GOOGLE_OAUTH_CLIENT_ID")
	clientSecret := getenv("GOOGLE_OAUTH_CLIENT_SECRET")
	redirectURL := getenv("GOOGLE_OAUTH_REDIRECT_URL")
	if clientID == "" || clientSecret == "" || redirectURL == "" {
		return nil, false
	}
	return &googleOAuth2Exchanger{
		cfg: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Scopes:       []string{"openid", "email", "profile"},
			Endpoint:     google.Endpoint,
		},
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}, true
}

func (g *googleOAuth2Exchanger) AuthCodeURL(state string) string {
	return g.cfg.AuthCodeURL(state)
}

// Exchange trades an authorization code for Google's access token, then
// calls the userinfo endpoint with it to get a server-verified subject and
// email — never trusting an unverified client-supplied claim.
func (g *googleOAuth2Exchanger) Exchange(ctx context.Context, code string) (*googleUserInfo, error) {
	token, err := g.cfg.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("exchange code: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, googleUserInfoURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build userinfo request: %w", err)
	}
	token.SetAuthHeader(req)

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch userinfo: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch userinfo: unexpected status %d", resp.StatusCode)
	}

	var body struct {
		Sub   string `json:"sub"`
		Email string `json:"email"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode userinfo: %w", err)
	}
	return &googleUserInfo{
		Subject: body.Sub,
		Email:   strings.ToLower(strings.TrimSpace(body.Email)),
	}, nil
}

// oauthStore is the minimal seam GoogleOAuthCallback needs around GORM to
// decide whether to reuse, link, or create a credential/identity pair.
// Unlike every other RPC in this codebase (which calls s.db directly — see
// backend/CLAUDE.md), this one exists specifically so the account-linking
// *decision* is unit-testable against an in-memory fake without a live
// database, since that decision is exactly the kind of logic that can't be
// verified "by eye." See oauth_test.go and docs/DECISIONS.md.
type oauthStore interface {
	// findIdentity returns (nil, nil) if no row matches — not an error —
	// mirroring gorm.ErrRecordNotFound's translation everywhere else a
	// "may not exist" lookup happens in this codebase.
	findIdentity(ctx context.Context, provider, subject string) (*OAuthIdentity, error)
	findCredentialByEmail(ctx context.Context, email string) (*Credential, error)
	createCredential(ctx context.Context, cred *Credential) error
	createIdentity(ctx context.Context, identity *OAuthIdentity) error
}

type gormOAuthStore struct{ db *gorm.DB }

func (g *gormOAuthStore) findIdentity(ctx context.Context, provider, subject string) (*OAuthIdentity, error) {
	var row OAuthIdentity
	err := g.db.WithContext(ctx).
		Where("provider = ? AND provider_subject = ?", provider, subject).
		First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

func (g *gormOAuthStore) findCredentialByEmail(ctx context.Context, email string) (*Credential, error) {
	var row Credential
	if err := g.db.WithContext(ctx).Where("email = ?", email).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

func (g *gormOAuthStore) createCredential(ctx context.Context, cred *Credential) error {
	return g.db.WithContext(ctx).Create(cred).Error
}

func (g *gormOAuthStore) createIdentity(ctx context.Context, identity *OAuthIdentity) error {
	return g.db.WithContext(ctx).Create(identity).Error
}

// GetGoogleAuthURL returns the URL a client should redirect the user's
// browser to. Returns FailedPrecondition if Google OAuth isn't configured
// on this instance (see NewGoogleExchangerFromEnv).
func (s *Server) GetGoogleAuthURL(_ context.Context, req *authv1.GetGoogleAuthURLRequest) (*authv1.GetGoogleAuthURLResponse, error) {
	if s.googleExchanger == nil {
		return nil, status.Error(codes.FailedPrecondition, "google oauth is not configured on this instance")
	}
	return &authv1.GetGoogleAuthURLResponse{
		AuthUrl: s.googleExchanger.AuthCodeURL(strings.TrimSpace(req.GetState())),
	}, nil
}

// GoogleOAuthCallback exchanges an authorization code for one of this
// platform's own access tokens, applying the account-linking rule
// documented on linkOrCreateGoogleUser. Returns FailedPrecondition if
// Google OAuth isn't configured, and Unavailable if oauthStore isn't
// configured (mirrors the db-nil degrade pattern everywhere else, but
// checks oauthStore specifically so a test can inject one without a real
// *gorm.DB — see oauth_test.go).
func (s *Server) GoogleOAuthCallback(ctx context.Context, req *authv1.GoogleOAuthCallbackRequest) (*authv1.GoogleOAuthCallbackResponse, error) {
	log := logger.FromContext(ctx, s.log)

	code := strings.TrimSpace(req.GetCode())
	if code == "" {
		return nil, status.Error(codes.InvalidArgument, "code is required")
	}
	if s.googleExchanger == nil {
		return nil, status.Error(codes.FailedPrecondition, "google oauth is not configured on this instance")
	}
	if s.oauthStore == nil {
		return nil, status.Error(codes.Unavailable, "database is not configured on this instance")
	}

	info, err := s.googleExchanger.Exchange(ctx, code)
	if err != nil {
		log.Warn("google token exchange failed", zap.Error(err))
		return nil, status.Error(codes.Unauthenticated, "failed to authenticate with google")
	}
	if info.Subject == "" || info.Email == "" {
		log.Error("google userinfo response missing subject or email")
		return nil, status.Error(codes.Internal, "google did not return a usable identity")
	}

	userID, err := s.linkOrCreateGoogleUser(ctx, info)
	if err != nil {
		log.Error("failed to resolve google identity", zap.Error(err))
		return nil, status.Error(codes.Internal, "failed to complete google sign-in")
	}

	token, err := jwks.Sign(s.keyPair, s.issuer, userID, info.Email, 0)
	if err != nil {
		log.Error("failed to sign token", zap.Error(err))
		return nil, status.Error(codes.Internal, "failed to issue a session")
	}

	log.Info("google oauth callback succeeded", zap.String("user_id", userID))
	return &authv1.GoogleOAuthCallbackResponse{AccessToken: token, UserId: userID}, nil
}

// linkOrCreateGoogleUser implements the account-linking rule (see
// docs/DECISIONS.md "OAuth account linking"):
//
//  1. An existing auth.oauth_identities row for this exact (provider,
//     subject) wins outright — this is a repeat Google login, reuse the
//     user_id it already points to.
//  2. Failing that, an existing auth.credentials row for the same email
//     (created via password Register) is linked to — a new
//     oauth_identities row is created pointing at that user_id, rather
//     than creating a second, duplicate account for the same person.
//  3. Failing both, a brand new credentials row (with an unusable random
//     password hash — see randomUnusablePasswordHash) and a new
//     oauth_identities row are created together.
//
// Email is deliberately never treated as authoritative on its own for
// step 1 — only the (provider, subject) pair is, since Google's subject
// is the one value guaranteed stable for a given Google Account even if
// its email later changes.
func (s *Server) linkOrCreateGoogleUser(ctx context.Context, info *googleUserInfo) (string, error) {
	if existing, err := s.oauthStore.findIdentity(ctx, ProviderGoogle, info.Subject); err != nil {
		return "", fmt.Errorf("look up existing identity: %w", err)
	} else if existing != nil {
		return existing.UserID, nil
	}

	userID, err := s.findOrCreateCredentialForEmail(ctx, info.Email)
	if err != nil {
		return "", err
	}

	identity := &OAuthIdentity{
		ID:              uuid.NewString(),
		UserID:          userID,
		Provider:        ProviderGoogle,
		ProviderSubject: info.Subject,
		Email:           info.Email,
	}
	if err := s.oauthStore.createIdentity(ctx, identity); err != nil {
		return "", fmt.Errorf("create oauth identity: %w", err)
	}
	return userID, nil
}

func (s *Server) findOrCreateCredentialForEmail(ctx context.Context, email string) (string, error) {
	existingCred, err := s.oauthStore.findCredentialByEmail(ctx, email)
	if err != nil {
		return "", fmt.Errorf("look up existing credential: %w", err)
	}
	if existingCred != nil {
		return existingCred.ID, nil
	}

	hash, err := randomUnusablePasswordHash()
	if err != nil {
		return "", err
	}
	cred := &Credential{ID: uuid.NewString(), Email: email, PasswordHash: hash}
	if err := s.oauthStore.createCredential(ctx, cred); err != nil {
		return "", fmt.Errorf("create credential: %w", err)
	}
	return cred.ID, nil
}

// randomUnusablePasswordHash returns a bcrypt hash of a cryptographically
// random value nobody can ever know, so a Google-only account still
// satisfies auth.credentials.password_hash's NOT NULL constraint without
// being a guessable password — Login's bcrypt.CompareHashAndPassword will
// simply never match anything a real client could send until the user
// separately sets a password (not built yet — out of Phase 1c's scope).
func randomUnusablePasswordHash() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate random password: %w", err)
	}
	hash, err := bcrypt.GenerateFromPassword(raw, bcryptCost)
	if err != nil {
		return "", fmt.Errorf("hash random password: %w", err)
	}
	return string(hash), nil
}
