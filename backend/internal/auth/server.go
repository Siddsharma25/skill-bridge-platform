package auth

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	authv1 "github.com/Siddsharma25/skill-bridge-platform/backend/gen/auth/v1"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/jwks"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/logger"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/rabbitmq"
)

// bcryptCost is deliberately the library default (10), not the max (31).
// Higher costs are more resistant to offline brute-force but also slow
// down every legitimate login; 10 is the widely-used floor that's still
// considered adequate as of 2026 and keeps local dev/test runs fast. See
// docs/DECISIONS.md.
const bcryptCost = bcrypt.DefaultCost

// Server implements authv1.AuthServiceServer. db may be nil — see
// docs/DECISIONS.md and cmd/auth-service/main.go: there is no live
// Supabase project yet, so the service starts without one and every RPC
// that needs it fails with a clear Unavailable status rather than the
// process crashing at startup.
type Server struct {
	authv1.UnimplementedAuthServiceServer

	db      *gorm.DB
	keyPair *jwks.KeyPair
	issuer  string
	log     *zap.Logger

	// googleExchanger is nil when Google OAuth isn't configured on this
	// instance (see NewGoogleExchangerFromEnv) — GetGoogleAuthURL and
	// GoogleOAuthCallback both degrade to FailedPrecondition rather than
	// the process failing to start. oauthStore is derived from db (nil
	// exactly when db is nil) except in tests, which inject a fake
	// directly — see oauth.go and oauth_test.go.
	googleExchanger GoogleExchanger
	oauthStore      oauthStore

	// rabbitPublisher is nil-safe (see rabbitmq.Producer.Publish) and may
	// be a *rabbitmq.Producer with no live connection, or a fake in tests —
	// same pattern as users.Server.publisher for Kafka. Register publishes
	// a best-effort "welcome" notification to it after the account is
	// created; a publish failure here never fails Register (see Register's
	// doc comment and docs/DECISIONS.md's no-transactional-outbox note).
	rabbitPublisher rabbitmq.Publisher
}

// NewServer constructs a Server. db, keyPair, and log must not be nil;
// issuer becomes the JWT `iss` claim. googleExchanger may be nil — see
// NewGoogleExchangerFromEnv — in which case the Google OAuth RPCs degrade
// to a clear FailedPrecondition status instead of the service failing to
// start. rabbitPublisher may also be nil, in which case Register simply
// skips publishing the welcome notification (checked explicitly at that
// call site, same pattern as users-service's AddUserSkill/Kafka).
func NewServer(db *gorm.DB, keyPair *jwks.KeyPair, issuer string, googleExchanger GoogleExchanger, rabbitPublisher rabbitmq.Publisher, log *zap.Logger) *Server {
	var store oauthStore
	if db != nil {
		store = &gormOAuthStore{db: db}
	}
	return &Server{
		db:              db,
		keyPair:         keyPair,
		issuer:          issuer,
		log:             log,
		googleExchanger: googleExchanger,
		oauthStore:      store,
		rabbitPublisher: rabbitPublisher,
	}
}

// Register hashes the password with bcrypt (never stored or logged in
// plaintext), inserts a new auth.credentials row, and returns a freshly
// signed access token so the caller can skip a second Login round trip.
func (s *Server) Register(ctx context.Context, req *authv1.RegisterRequest) (*authv1.RegisterResponse, error) {
	log := logger.FromContext(ctx, s.log)

	email := strings.ToLower(strings.TrimSpace(req.GetEmail()))
	if email == "" || req.GetPassword() == "" {
		return nil, status.Error(codes.InvalidArgument, "email and password are required")
	}
	if len(req.GetPassword()) < 8 {
		return nil, status.Error(codes.InvalidArgument, "password must be at least 8 characters")
	}
	if s.db == nil {
		return nil, status.Error(codes.Unavailable, "database is not configured on this instance")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.GetPassword()), bcryptCost)
	if err != nil {
		log.Error("bcrypt hash failed", zap.Error(err))
		return nil, status.Error(codes.Internal, "failed to process password")
	}

	cred := Credential{
		ID:           uuid.NewString(),
		Email:        email,
		PasswordHash: string(hash),
		Role:         RoleUser,
	}
	if err := s.db.WithContext(ctx).Create(&cred).Error; err != nil {
		if isDuplicateKey(err) {
			return nil, status.Error(codes.AlreadyExists, "an account with this email already exists")
		}
		log.Error("failed to insert credential", zap.Error(err))
		return nil, status.Error(codes.Internal, "failed to create account")
	}

	token, err := jwks.Sign(s.keyPair, s.issuer, cred.ID, cred.Email, cred.Role, 0)
	if err != nil {
		log.Error("failed to sign token", zap.Error(err))
		return nil, status.Error(codes.Internal, "account created but failed to issue a session")
	}

	// Best-effort welcome notification (Phase 3): the account is already
	// durably created in Postgres above, so nothing here — a nil
	// publisher, a marshal error, or a broker publish failure — is allowed
	// to fail Register. See rabbitmq.Producer.Publish's doc comment for
	// why a publish failure specifically logs at Error, not Warn, despite
	// still not propagating: this is real (if recoverable) data loss for
	// the notification, not a pure performance optimization failing safe.
	if s.rabbitPublisher != nil {
		payload, marshalErr := json.Marshal(rabbitmq.EmailNotification{
			MessageID: uuid.NewString(),
			UserID:    cred.ID,
			Email:     cred.Email,
			EventType: rabbitmq.EventTypeWelcome,
			CreatedAt: time.Now().UTC(),
		})
		if marshalErr != nil {
			log.Error("failed to marshal welcome notification payload; skipping publish", zap.Error(marshalErr))
		} else {
			s.rabbitPublisher.Publish(ctx, rabbitmq.QueueNotificationsEmail, payload)
		}

		// Best-effort realtime "welcome" ping (Phase 3.5) — architecturally
		// complete (this is the "auth-service on successful Register"
		// publisher the plan calls for) but NOT this phase's live
		// verification path: this publish happens synchronously, right
		// here, before the RPC has even returned the freshly issued access
		// token to the caller, so no client could possibly have opened a
		// WebSocket onNotification subscription for cred.ID yet (there's
		// no token to authenticate one with until Register returns) — and
		// Redis pub/sub has no replay buffer, so a publish with no live
		// subscriber is simply lost by design. See
		// docs/DECISIONS.md's Phase 3.5 notes for the full reasoning and
		// why jobs-service's job.matched publish (matcher.go) is used for
		// live verification instead. Still published unconditionally here
		// (not gated behind some "is anyone likely listening" check this
		// codebase has no way to answer) since a real client that logs in
		// again shortly after registering, or a future phase's UI that
		// opens the subscription before registration completes, could
		// still benefit from it landing.
		realtimePayload, marshalErr := json.Marshal(rabbitmq.RealtimeNotification{
			ID:        uuid.NewString(),
			UserID:    cred.ID,
			Type:      rabbitmq.RealtimeNotificationTypeWelcome,
			Message:   "Welcome to Skill Bridge!",
			CreatedAt: time.Now().UTC(),
		})
		if marshalErr != nil {
			log.Error("failed to marshal realtime welcome notification payload; skipping publish", zap.Error(marshalErr))
		} else {
			s.rabbitPublisher.Publish(ctx, rabbitmq.QueueNotificationsRealtime, realtimePayload)
		}
	}

	log.Info("registered new credential", zap.String("user_id", cred.ID))
	return &authv1.RegisterResponse{
		UserId:      cred.ID,
		AccessToken: token,
	}, nil
}

// Login verifies the supplied password against the stored bcrypt hash. It
// deliberately returns the same Unauthenticated status/message whether the
// email doesn't exist or the password is wrong — distinguishing the two is
// a user-enumeration side channel.
func (s *Server) Login(ctx context.Context, req *authv1.LoginRequest) (*authv1.LoginResponse, error) {
	log := logger.FromContext(ctx, s.log)

	email := strings.ToLower(strings.TrimSpace(req.GetEmail()))
	if email == "" || req.GetPassword() == "" {
		return nil, status.Error(codes.InvalidArgument, "email and password are required")
	}
	if s.db == nil {
		return nil, status.Error(codes.Unavailable, "database is not configured on this instance")
	}

	const invalidCredsMsg = "invalid email or password"

	var cred Credential
	if err := s.db.WithContext(ctx).Where("email = ?", email).First(&cred).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.Unauthenticated, invalidCredsMsg)
		}
		log.Error("failed to look up credential", zap.Error(err))
		return nil, status.Error(codes.Internal, "failed to process login")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(cred.PasswordHash), []byte(req.GetPassword())); err != nil {
		return nil, status.Error(codes.Unauthenticated, invalidCredsMsg)
	}

	token, err := jwks.Sign(s.keyPair, s.issuer, cred.ID, cred.Email, cred.Role, 0)
	if err != nil {
		log.Error("failed to sign token", zap.Error(err))
		return nil, status.Error(codes.Internal, "failed to issue a session")
	}

	log.Info("login succeeded", zap.String("user_id", cred.ID))
	return &authv1.LoginResponse{AccessToken: token}, nil
}

// isDuplicateKey reports whether err is a Postgres unique-violation
// (SQLSTATE 23505), which is how a duplicate-email Create surfaces through
// GORM's postgres/pgx driver.
func isDuplicateKey(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}
