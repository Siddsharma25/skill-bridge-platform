package auth

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	authv1 "github.com/Siddsharma25/skill-bridge-platform/backend/gen/auth/v1"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/jwks"
)

// These tests never make a real network call to Google — fakeExchanger
// stands in for GoogleExchanger (see oauth.go) with canned responses, per
// the task's explicit ask: verify the account-linking logic with a
// fake/mocked token exchange, since there's no live Google Cloud OAuth
// client to test against yet (see docs/DECISIONS.md). They also never
// touch a real database — fakeOAuthStore stands in for oauthStore — since
// the thing under test here is the *decision* (link vs. create vs. reuse),
// not GORM/SQL correctness; that part of the code (gormOAuthStore) is a
// handful of lines that mirror every other lookup in this package and was
// verified manually against a throwaway Postgres container, the same way
// every other DB-touching path in this codebase is (see
// docs/DECISIONS.md's Phase 1a/1b notes).

type fakeExchanger struct {
	info     *googleUserInfo
	err      error
	authURL  string
	lastCode string
}

func (f *fakeExchanger) AuthCodeURL(state string) string { return f.authURL + "?state=" + state }

func (f *fakeExchanger) Exchange(_ context.Context, code string) (*googleUserInfo, error) {
	f.lastCode = code
	if f.err != nil {
		return nil, f.err
	}
	return f.info, nil
}

// fakeOAuthStore is a map-based in-memory stand-in for gormOAuthStore. It
// tracks how many times each write method is called so tests can assert
// exactly one credential/identity was created (or none, when linking or
// reusing) — the same "don't just trust it, prove it" bar the cache-hit
// test applies to skills/jobs-service.
type fakeOAuthStore struct {
	credentials           []*Credential
	identities            []*OAuthIdentity
	createCredentialCalls int
	createIdentityCalls   int
}

func (f *fakeOAuthStore) findIdentity(_ context.Context, provider, subject string) (*OAuthIdentity, error) {
	for _, id := range f.identities {
		if id.Provider == provider && id.ProviderSubject == subject {
			return id, nil
		}
	}
	return nil, nil
}

func (f *fakeOAuthStore) findCredentialByEmail(_ context.Context, email string) (*Credential, error) {
	for _, c := range f.credentials {
		if c.Email == email {
			return c, nil
		}
	}
	return nil, nil
}

func (f *fakeOAuthStore) createCredential(_ context.Context, cred *Credential) error {
	f.createCredentialCalls++
	f.credentials = append(f.credentials, cred)
	return nil
}

func (f *fakeOAuthStore) createIdentity(_ context.Context, identity *OAuthIdentity) error {
	f.createIdentityCalls++
	f.identities = append(f.identities, identity)
	return nil
}

func testOAuthServer(t *testing.T, exchanger GoogleExchanger, store oauthStore) *Server {
	t.Helper()
	kp, err := jwks.GenerateKeyPair()
	if err != nil {
		t.Fatalf("failed to generate test keypair: %v", err)
	}
	return &Server{
		keyPair:         kp,
		issuer:          "test-issuer",
		log:             zap.NewNop(),
		googleExchanger: exchanger,
		oauthStore:      store,
	}
}

func TestGetGoogleAuthURL_NotConfiguredReturnsFailedPrecondition(t *testing.T) {
	s := testOAuthServer(t, nil, &fakeOAuthStore{})
	_, err := s.GetGoogleAuthURL(context.Background(), &authv1.GetGoogleAuthURLRequest{State: "abc"})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition, got %v", err)
	}
}

func TestGetGoogleAuthURL_ReturnsURLWithState(t *testing.T) {
	ex := &fakeExchanger{authURL: "https://accounts.google.com/o/oauth2/auth"}
	s := testOAuthServer(t, ex, &fakeOAuthStore{})
	resp, err := s.GetGoogleAuthURL(context.Background(), &authv1.GetGoogleAuthURLRequest{State: "xyz123"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://accounts.google.com/o/oauth2/auth?state=xyz123"
	if resp.GetAuthUrl() != want {
		t.Fatalf("expected auth_url %q, got %q", want, resp.GetAuthUrl())
	}
}

func TestGoogleOAuthCallback_RejectsEmptyCode(t *testing.T) {
	s := testOAuthServer(t, &fakeExchanger{}, &fakeOAuthStore{})
	_, err := s.GoogleOAuthCallback(context.Background(), &authv1.GoogleOAuthCallbackRequest{Code: ""})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
}

func TestGoogleOAuthCallback_NotConfiguredReturnsFailedPrecondition(t *testing.T) {
	s := testOAuthServer(t, nil, &fakeOAuthStore{})
	_, err := s.GoogleOAuthCallback(context.Background(), &authv1.GoogleOAuthCallbackRequest{Code: "some-code"})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition, got %v", err)
	}
}

func TestGoogleOAuthCallback_NoStoreReturnsUnavailable(t *testing.T) {
	s := testOAuthServer(t, &fakeExchanger{info: &googleUserInfo{Subject: "sub", Email: "a@example.com"}}, nil)
	_, err := s.GoogleOAuthCallback(context.Background(), &authv1.GoogleOAuthCallbackRequest{Code: "some-code"})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("expected Unavailable, got %v", err)
	}
}

func TestGoogleOAuthCallback_ExchangeFailureReturnsUnauthenticated(t *testing.T) {
	s := testOAuthServer(t, &fakeExchanger{err: errors.New("boom")}, &fakeOAuthStore{})
	_, err := s.GoogleOAuthCallback(context.Background(), &authv1.GoogleOAuthCallbackRequest{Code: "some-code"})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got %v", err)
	}
}

// TestGoogleOAuthCallback_CreatesNewUserOnFirstLogin covers the "neither an
// identity nor a credential exists yet" branch: a brand new credential and
// a brand new oauth identity are both created, linked to each other.
func TestGoogleOAuthCallback_CreatesNewUserOnFirstLogin(t *testing.T) {
	store := &fakeOAuthStore{}
	ex := &fakeExchanger{info: &googleUserInfo{Subject: "google-sub-1", Email: "brandnew@example.com"}}
	s := testOAuthServer(t, ex, store)

	resp, err := s.GoogleOAuthCallback(context.Background(), &authv1.GoogleOAuthCallbackRequest{Code: "auth-code"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.GetUserId() == "" || resp.GetAccessToken() == "" {
		t.Fatalf("expected non-empty user_id and access_token, got %+v", resp)
	}
	if store.createCredentialCalls != 1 {
		t.Fatalf("expected exactly 1 credential created, got %d", store.createCredentialCalls)
	}
	if store.createIdentityCalls != 1 {
		t.Fatalf("expected exactly 1 identity created, got %d", store.createIdentityCalls)
	}
	if store.identities[0].UserID != resp.GetUserId() {
		t.Fatalf("identity.UserID %q does not match returned user_id %q", store.identities[0].UserID, resp.GetUserId())
	}
}

// TestGoogleOAuthCallback_LinksToExistingCredentialByEmail is the
// account-linking test the task specifically calls for: a user who
// registered by password already has an auth.credentials row; logging in
// with Google using the *same email* must link a new oauth_identities row
// to that existing user_id, never create a second, duplicate account.
func TestGoogleOAuthCallback_LinksToExistingCredentialByEmail(t *testing.T) {
	existing := &Credential{ID: "existing-user-id-123", Email: "shared@example.com", PasswordHash: "irrelevant-hash"}
	store := &fakeOAuthStore{credentials: []*Credential{existing}}
	ex := &fakeExchanger{info: &googleUserInfo{Subject: "google-sub-2", Email: "shared@example.com"}}
	s := testOAuthServer(t, ex, store)

	resp, err := s.GoogleOAuthCallback(context.Background(), &authv1.GoogleOAuthCallbackRequest{Code: "auth-code"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.GetUserId() != existing.ID {
		t.Fatalf("expected linked user_id %q (the existing password account), got %q", existing.ID, resp.GetUserId())
	}
	// No new credential should have been created — linking, not
	// duplicating.
	if store.createCredentialCalls != 0 {
		t.Fatalf("expected 0 new credentials created when linking, got %d", store.createCredentialCalls)
	}
	if len(store.credentials) != 1 {
		t.Fatalf("expected exactly 1 credential to still exist, got %d", len(store.credentials))
	}
	// Exactly one new identity should have been created, linking Google's
	// subject to the pre-existing credential.
	if store.createIdentityCalls != 1 {
		t.Fatalf("expected exactly 1 identity created, got %d", store.createIdentityCalls)
	}
	if store.identities[0].UserID != existing.ID {
		t.Fatalf("new identity should point at existing user_id %q, got %q", existing.ID, store.identities[0].UserID)
	}
}

// TestGoogleOAuthCallback_ReusesExistingLinkedIdentity covers a repeat
// Google login: the (provider, subject) identity already exists, so no new
// credential or identity row should be created at all — just reuse the
// user_id the identity already points to.
func TestGoogleOAuthCallback_ReusesExistingLinkedIdentity(t *testing.T) {
	store := &fakeOAuthStore{
		credentials: []*Credential{{ID: "user-42", Email: "repeat@example.com", PasswordHash: "irrelevant-hash"}},
		identities: []*OAuthIdentity{
			{ID: "identity-1", UserID: "user-42", Provider: ProviderGoogle, ProviderSubject: "google-sub-3", Email: "repeat@example.com"},
		},
	}
	ex := &fakeExchanger{info: &googleUserInfo{Subject: "google-sub-3", Email: "repeat@example.com"}}
	s := testOAuthServer(t, ex, store)

	resp, err := s.GoogleOAuthCallback(context.Background(), &authv1.GoogleOAuthCallbackRequest{Code: "auth-code"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.GetUserId() != "user-42" {
		t.Fatalf("expected user_id %q, got %q", "user-42", resp.GetUserId())
	}
	if store.createCredentialCalls != 0 || store.createIdentityCalls != 0 {
		t.Fatalf("expected no new rows created on repeat login, got %d credentials and %d identities",
			store.createCredentialCalls, store.createIdentityCalls)
	}
}
