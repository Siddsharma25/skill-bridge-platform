// Command users-service is the gRPC service that owns profile data and a
// user's claimed skills. See backend/cmd/users-service/README.md for the
// why behind its design, including the lazy-profile-creation note.
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

	usersv1 "github.com/Siddsharma25/skill-bridge-platform/backend/gen/users/v1"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/db"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/health"
	kafkaplat "github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/kafka"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/logger"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/requestid"
	sentryplat "github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/sentry"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/shutdown"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/users"
)

func main() {
	env := os.Getenv("ENV")
	if env != "production" {
		_ = godotenv.Load()
	}

	log, err := logger.New("users-service")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to build logger: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = log.Sync() }()

	// Sentry (error tracking + log capture): degrades gracefully, same
	// pattern as every other optional dependency in this file. See
	// internal/platform/sentry and docs/DECISIONS.md's Sentry section.
	sentryHandle := sentryplat.InitFromEnv(os.Getenv, "users-service", log)
	defer sentryHandle.Flush(2 * time.Second)
	log = log.WithOptions(zap.WrapCore(sentryHandle.WrapCore))

	grpcPort := envOr("PORT", "9003")
	httpPort := envOr("HTTP_PORT", "8083")

	// Connect to Postgres, but degrade gracefully rather than exit — same
	// pattern as every other service in this codebase (see
	// docs/DECISIONS.md and backend/CLAUDE.md).
	dbCfg := db.LoadConfigFromEnv()
	var gormDB *gorm.DB
	gormConn, dbErr := db.Connect(dbCfg)
	if dbErr != nil {
		log.Warn("starting without a database connection; profile/skill RPCs will return Unavailable until DATABASE_URL is set and reachable",
			zap.Error(dbErr))
	} else {
		gormDB = gormConn
		log.Info("connected to database")
	}

	// Kafka producer (Phase 2): degrades gracefully, same pattern as the
	// DB connection above — a missing/unreachable KAFKA_BROKERS just
	// disables event publishing for this instance rather than failing to
	// start. See internal/platform/kafka and docs/DECISIONS.md.
	kafkaProducer := kafkaplat.NewProducerFromEnv(os.Getenv, log)

	usersServer := users.NewServer(gormDB, kafkaProducer, log)

	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(requestid.UnaryServerInterceptor(), sentryHandle.UnaryServerInterceptor()),
	)
	usersv1.RegisterUsersServiceServer(grpcServer, usersServer)
	health.NewGRPCServer(grpcServer)
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
				kafkaProducer.Close()
				return nil
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
