package auth

import (
	"context"
	"errors"
	"strings"

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
}

// NewServer constructs a Server. db, keyPair, and log must not be nil;
// issuer becomes the JWT `iss` claim.
func NewServer(db *gorm.DB, keyPair *jwks.KeyPair, issuer string, log *zap.Logger) *Server {
	return &Server{db: db, keyPair: keyPair, issuer: issuer, log: log}
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
	}
	if err := s.db.WithContext(ctx).Create(&cred).Error; err != nil {
		if isDuplicateKey(err) {
			return nil, status.Error(codes.AlreadyExists, "an account with this email already exists")
		}
		log.Error("failed to insert credential", zap.Error(err))
		return nil, status.Error(codes.Internal, "failed to create account")
	}

	token, err := jwks.Sign(s.keyPair, s.issuer, cred.ID, cred.Email, 0)
	if err != nil {
		log.Error("failed to sign token", zap.Error(err))
		return nil, status.Error(codes.Internal, "account created but failed to issue a session")
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

	token, err := jwks.Sign(s.keyPair, s.issuer, cred.ID, cred.Email, 0)
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
