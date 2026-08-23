// Command auth-service is the gRPC service that owns password credentials
// and JWT issuance for the whole platform. See
// backend/cmd/auth-service/README.md for the why behind its design.
package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/joho/godotenv"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	"gorm.io/gorm"

	authv1 "github.com/Siddsharma25/skill-bridge-platform/backend/gen/auth/v1"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/auth"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/db"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/health"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/jwks"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/logger"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/requestid"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/shutdown"
)

func main() {
	env := os.Getenv("ENV")
	if env != "production" {
		// Local dev convenience only; see backend/.env.example. Ignored if
		// the file doesn't exist — that's expected in CI and in
		// production.
		_ = godotenv.Load()
	}

	log, err := logger.New("auth-service")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to build logger: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = log.Sync() }()

	grpcPort := envOr("PORT", "9001")
	httpPort := envOr("HTTP_PORT", "8081")
	issuer := envOr("JWT_ISSUER", "skill-bridge-platform/auth-service")

	// Connect to Postgres, but degrade gracefully rather than exit: there
	// is no live Supabase project yet at this phase of the build (see
	// docs/DECISIONS.md), and the service should still start and serve
	// health/JWKS endpoints so the rest of the plumbing (gateway startup,
	// CI, local smoke tests) isn't blocked on an external dependency that
	// doesn't exist yet.
	dbCfg := db.LoadConfigFromEnv()
	var gormDB *gorm.DB
	gormConn, dbErr := db.Connect(dbCfg)
	if dbErr != nil {
		log.Warn("starting without a database connection; Register/Login will return Unavailable until DATABASE_URL is set and reachable",
			zap.Error(dbErr))
	} else {
		gormDB = gormConn
		log.Info("connected to database")
	}

	keyPair, err := jwks.LoadOrGenerate(os.Getenv)
	if err != nil {
		log.Fatal("failed to load or generate JWT keypair", zap.Error(err))
	}
	if os.Getenv("JWT_PRIVATE_KEY_PEM") == "" {
		log.Warn("JWT_PRIVATE_KEY_PEM not set; generated an ephemeral RS256 keypair for this process only " +
			"(dev convenience) — tokens signed by this instance won't verify after a restart, and won't " +
			"match a different replica in a multi-instance deployment. Set JWT_PRIVATE_KEY_PEM in production.")
	}

	authServer := auth.NewServer(gormDB, keyPair, issuer, log)

	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(requestid.UnaryServerInterceptor()),
	)
	authv1.RegisterAuthServiceServer(grpcServer, authServer)
	health.NewGRPCServer(grpcServer)
	// Reflection makes grpcurl/buf curl usable without shipping .proto
	// files to whoever's debugging — the plan's recommended way to poke at
	// services that don't have a REST surface (only skills-service gets
	// grpc-gateway, per docs/DECISIONS.md). Safe on a private network;
	// gate behind ENV if this is ever reachable publicly.
	reflection.Register(grpcServer)

	lis, err := net.Listen("tcp", ":"+grpcPort)
	if err != nil {
		log.Fatal("failed to listen for gRPC", zap.String("port", grpcPort), zap.Error(err))
	}
	go func() {
		log.Info("gRPC server listening", zap.String("port", grpcPort))
		if err := grpcServer.Serve(lis); err != nil && err != grpc.ErrServerStopped {
			log.Error("gRPC server stopped unexpectedly", zap.Error(err))
		}
	}()

	mux := health.Mux(func(_ context.Context) error {
		if gormDB == nil {
			return fmt.Errorf("database is not configured")
		}
		return db.Ping(gormDB)
	})
	mux.HandleFunc("/.well-known/jwks.json", jwks.Handler(keyPair))

	httpServer := &http.Server{
		Addr:              ":" + httpPort,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Info("HTTP server listening (health + JWKS)", zap.String("port", httpPort))
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("HTTP server stopped unexpectedly", zap.Error(err))
		}
	}()

	shutdown.Wait(context.Background(), shutdown.Options{
		Logger:      log,
		GRPCServer:  grpcServer,
		HTTPServers: []*http.Server{httpServer},
		Cleanups: []shutdown.CleanupFunc{
			func(_ context.Context) error {
				if gormDB == nil {
					return nil
				}
				sqlDB, err := gormDB.DB()
				if err != nil {
					return err
				}
				return sqlDB.Close()
			},
		},
	})
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
