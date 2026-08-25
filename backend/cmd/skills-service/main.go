// Command skills-service is the gRPC service that owns the platform's
// skill taxonomy. See backend/cmd/skills-service/README.md for the why
// behind its design.
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

	skillsv1 "github.com/Siddsharma25/skill-bridge-platform/backend/gen/skills/v1"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/cache"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/db"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/health"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/logger"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/requestid"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/shutdown"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/skills"
)

func main() {
	env := os.Getenv("ENV")
	if env != "production" {
		_ = godotenv.Load()
	}

	log, err := logger.New("skills-service")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to build logger: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = log.Sync() }()

	grpcPort := envOr("PORT", "9002")
	httpPort := envOr("HTTP_PORT", "8082")

	// Connect to Postgres, but degrade gracefully rather than exit — same
	// pattern as auth-service (see docs/DECISIONS.md and backend/CLAUDE.md):
	// the service starts and serves health checks even without a reachable
	// database, and every RPC that needs one returns Unavailable instead.
	dbCfg := db.LoadConfigFromEnv()
	var gormDB *gorm.DB
	gormConn, dbErr := db.Connect(dbCfg)
	if dbErr != nil {
		log.Warn("starting without a database connection; CreateSkill/ListSkills will return Unavailable until DATABASE_URL is set and reachable",
			zap.Error(dbErr))
	} else {
		gormDB = gormConn
		log.Info("connected to database")
	}

	// Redis cache (Phase 1c): degrades gracefully, same pattern as the DB
	// connection above — a missing/unreachable REDIS_URL just disables
	// caching for this instance rather than failing to start. See
	// internal/platform/cache and docs/DECISIONS.md.
	redisCache := cache.NewFromEnv(os.Getenv, log)

	skillsServer := skills.NewServer(gormDB, redisCache, log)

	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(requestid.UnaryServerInterceptor()),
	)
	skillsv1.RegisterSkillsServiceServer(grpcServer, skillsServer)
	health.NewGRPCServer(grpcServer)
	// Reflection enabled on every gRPC service in this codebase, per
	// docs/DECISIONS.md — makes grpcurl/buf curl usable without shipping
	// .proto files around.
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

	httpServer := &http.Server{
		Addr:              ":" + httpPort,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Info("HTTP server listening (health)", zap.String("port", httpPort))
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
			func(_ context.Context) error {
				return redisCache.Close()
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
