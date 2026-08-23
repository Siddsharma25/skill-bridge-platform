// Command api-gateway is the public-facing GraphQL edge of the platform.
// It holds no state of its own — every mutation/query is a thin
// pass-through to a backend gRPC service. See
// backend/internal/gateway/README.md (and docs/DECISIONS.md) for why
// GraphQL-over-gRPC rather than exposing gRPC or REST directly to the
// frontend.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/playground"
	"github.com/joho/godotenv"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	authv1 "github.com/Siddsharma25/skill-bridge-platform/backend/gen/auth/v1"
	jobsv1 "github.com/Siddsharma25/skill-bridge-platform/backend/gen/jobs/v1"
	skillsv1 "github.com/Siddsharma25/skill-bridge-platform/backend/gen/skills/v1"
	usersv1 "github.com/Siddsharma25/skill-bridge-platform/backend/gen/users/v1"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/gateway/authctx"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/gateway/graph"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/gateway/graph/generated"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/health"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/jwks"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/logger"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/requestid"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/shutdown"
)

func main() {
	env := os.Getenv("ENV")
	if env != "production" {
		_ = godotenv.Load()
	}

	log, err := logger.New("api-gateway")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to build logger: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = log.Sync() }()

	httpPort := envOr("PORT", "8080")
	authServiceAddr := envOr("AUTH_SERVICE_ADDR", "localhost:9001")
	authJWKSURL := envOr("AUTH_SERVICE_JWKS_URL", "http://localhost:8081/.well-known/jwks.json")
	skillsServiceAddr := envOr("SKILLS_SERVICE_ADDR", "localhost:9002")
	usersServiceAddr := envOr("USERS_SERVICE_ADDR", "localhost:9003")
	jobsServiceAddr := envOr("JOBS_SERVICE_ADDR", "localhost:9004")

	// Dial auth-service. grpc.NewClient (not the deprecated blocking
	// DialContext+WithBlock) connects lazily — the gateway starts even if
	// auth-service isn't up yet, and gRPC transparently reconnects/retries
	// per-call. Request-ID propagation rides on every call via the client
	// interceptor, matching the server interceptor on auth-service's side.
	authConn, err := grpc.NewClient(
		authServiceAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(requestid.UnaryClientInterceptor()),
	)
	if err != nil {
		log.Fatal("failed to create auth-service gRPC client", zap.Error(err))
	}
	authClient := authv1.NewAuthServiceClient(authConn)

	// skills-service and jobs-service stay unauthenticated in Phase 1b (no
	// role system yet — see docs/DECISIONS.md), so their client
	// connections only carry request-ID propagation, same as auth-service.
	skillsConn, err := grpc.NewClient(
		skillsServiceAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(requestid.UnaryClientInterceptor()),
	)
	if err != nil {
		log.Fatal("failed to create skills-service gRPC client", zap.Error(err))
	}
	skillsClient := skillsv1.NewSkillsServiceClient(skillsConn)

	jobsConn, err := grpc.NewClient(
		jobsServiceAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(requestid.UnaryClientInterceptor()),
	)
	if err != nil {
		log.Fatal("failed to create jobs-service gRPC client", zap.Error(err))
	}
	jobsClient := jobsv1.NewJobsServiceClient(jobsConn)

	// users-service's client connection additionally carries the verified
	// caller's user ID (set by authctx.Middleware below) as outgoing gRPC
	// metadata — myProfile/updateProfile/addUserSkill are the first
	// authenticated operations in this project (see docs/DECISIONS.md).
	usersConn, err := grpc.NewClient(
		usersServiceAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(requestid.UnaryClientInterceptor(), authctx.UnaryClientInterceptor()),
	)
	if err != nil {
		log.Fatal("failed to create users-service gRPC client", zap.Error(err))
	}
	usersClient := usersv1.NewUsersServiceClient(usersConn)

	// Fetch auth-service's JWKS once at startup and keep the client around
	// for request-time verification — see internal/gateway/authctx, the
	// first phase this plumbing is actually wired into request handling
	// (Phase 1a only proved the fetch/cache path worked).
	jwksClient := jwks.NewClient(authJWKSURL, 0)
	startupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := jwksClient.Refresh(startupCtx); err != nil {
		log.Warn("failed to fetch auth-service JWKS at startup; will retry lazily on first use",
			zap.String("url", authJWKSURL), zap.Error(err))
	} else {
		log.Info("fetched auth-service JWKS", zap.String("url", authJWKSURL))
	}
	cancel()

	resolver := &graph.Resolver{
		AuthClient:   authClient,
		SkillsClient: skillsClient,
		UsersClient:  usersClient,
		JobsClient:   jobsClient,
	}
	srv := handler.NewDefaultServer(generated.NewExecutableSchema(generated.Config{Resolvers: resolver}))

	mux := health.Mux(func(ctx context.Context) error {
		// api-gateway has no database of its own; readiness here means
		// "can I still reach auth-service," which is the one hard
		// dependency Phase 1a's gateway has.
		pingCtx, pingCancel := context.WithTimeout(ctx, 2*time.Second)
		defer pingCancel()
		_, err := authClient.Login(pingCtx, &authv1.LoginRequest{Email: "", Password: ""})
		// Any response (including a well-formed InvalidArgument/
		// Unauthenticated error) proves the connection is alive; only a
		// transport-level failure means auth-service is unreachable.
		if err == nil {
			return nil
		}
		if isTransportError(err) {
			return err
		}
		return nil
	})

	if env != "production" {
		mux.Handle("/", playground.Handler("GraphQL Playground", "/query"))
		log.Info("GraphQL Playground enabled at /")
	}
	// authctx.Middleware extracts and verifies a bearer token (if any)
	// before the GraphQL handler runs, so myProfile/updateProfile/
	// addUserSkill's requireUserID check (schema.resolvers.go) can read
	// the verified caller — see docs/DECISIONS.md.
	mux.Handle("/query", authctx.Middleware(jwksClient, log)(srv))

	httpServer := &http.Server{
		Addr:              ":" + httpPort,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Info("api-gateway HTTP server listening", zap.String("port", httpPort))
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("HTTP server stopped unexpectedly", zap.Error(err))
		}
	}()

	shutdown.Wait(context.Background(), shutdown.Options{
		Logger:      log,
		HTTPServers: []*http.Server{httpServer},
		Cleanups: []shutdown.CleanupFunc{
			func(_ context.Context) error {
				return authConn.Close()
			},
			func(_ context.Context) error {
				return skillsConn.Close()
			},
			func(_ context.Context) error {
				return usersConn.Close()
			},
			func(_ context.Context) error {
				return jobsConn.Close()
			},
		},
	})
}

// isTransportError reports whether err looks like a connection-level
// failure (auth-service unreachable) rather than a well-formed gRPC
// business error (which itself proves the connection works).
func isTransportError(err error) bool {
	return status.Code(err) == codes.Unavailable
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
