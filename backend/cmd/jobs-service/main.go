// Command jobs-service is the gRPC service that owns job postings and the
// skills they require. See backend/cmd/jobs-service/README.md for the why
// behind its design.
package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/joho/godotenv"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	"gorm.io/gorm"

	jobsv1 "github.com/Siddsharma25/skill-bridge-platform/backend/gen/jobs/v1"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/jobs"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/cache"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/db"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/health"
	kafkaplat "github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/kafka"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/logger"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/requestid"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/shutdown"
)

// Consumer group IDs. Each is a separate franz-go client/consumer group so
// each responsibility (snapshot projection, matching worker, cache
// invalidation) can be scaled, restarted, or fail independently of the
// others — see docs/DECISIONS.md's Phase 2 notes.
const (
	snapshotConsumerGroup         = "jobs-service.user-skill-snapshot"
	matcherConsumerGroup          = "jobs-service.matching-worker"
	cacheInvalidatorConsumerGroup = "jobs-service.cache-invalidator"
)

func main() {
	env := os.Getenv("ENV")
	if env != "production" {
		_ = godotenv.Load()
	}

	log, err := logger.New("jobs-service")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to build logger: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = log.Sync() }()

	grpcPort := envOr("PORT", "9004")
	httpPort := envOr("HTTP_PORT", "8084")

	// Connect to Postgres, but degrade gracefully rather than exit — same
	// pattern as every other service in this codebase (see
	// docs/DECISIONS.md and backend/CLAUDE.md).
	dbCfg := db.LoadConfigFromEnv()
	var gormDB *gorm.DB
	gormConn, dbErr := db.Connect(dbCfg)
	if dbErr != nil {
		log.Warn("starting without a database connection; job RPCs will return Unavailable until DATABASE_URL is set and reachable",
			zap.Error(dbErr))
	} else {
		gormDB = gormConn
		log.Info("connected to database")
	}

	// Redis cache (Phase 1c): degrades gracefully, same pattern as the DB
	// connection above. See internal/platform/cache and docs/DECISIONS.md.
	redisCache := cache.NewFromEnv(os.Getenv, log)

	// Kafka producer (Phase 2): publishes job.posted (CreateJob) and
	// job.matched (the matching worker, below). Degrades gracefully, same
	// pattern as DB/Redis. See internal/platform/kafka and
	// docs/DECISIONS.md.
	kafkaProducer := kafkaplat.NewProducerFromEnv(os.Getenv, log)

	jobsServer := jobs.NewServer(gormDB, redisCache, kafkaProducer, log)

	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(requestid.UnaryServerInterceptor()),
	)
	jobsv1.RegisterJobsServiceServer(grpcServer, jobsServer)
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

	// --- Phase 2: three Kafka consumers running alongside the gRPC
	// server, each its own consumer group, all in this same process ---
	//
	//  1. snapshot: consumes user.skills.updated, maintains
	//     jobs.user_skill_snapshot (see internal/jobs/snapshot.go).
	//  2. matcher: consumes job.posted (the "matching worker" the plan
	//     calls for — see internal/jobs/matcher.go), scores candidates
	//     from the snapshot, upserts jobs.job_matches, publishes
	//     job.matched.
	//  3. cacheInvalidator: consumes skills-service's skill.updated,
	//     evicts jobs:all (see internal/jobs/cache_invalidation.go) — the
	//     one genuinely cross-service cache-invalidation case in this
	//     codebase (docs/DECISIONS.md).
	//
	// All three degrade gracefully exactly like the DB/Redis/producer
	// above: if KAFKA_BROKERS isn't set, NewConsumerFromEnv returns
	// ErrNotConfigured and that consumer simply doesn't run on this
	// instance (logged clearly), rather than the process failing to
	// start.
	consumerCtx, cancelConsumers := context.WithCancel(context.Background())
	var consumerWG sync.WaitGroup

	snapshotConsumer := startConsumer(consumerCtx, &consumerWG, log, snapshotConsumerGroup,
		[]string{kafkaplat.TopicUserSkillsUpdated},
		jobs.HandleUserSkillsUpdated(jobs.NewGormSnapshotStore(gormDB), log))

	matcherConsumer := startConsumer(consumerCtx, &consumerWG, log, matcherConsumerGroup,
		[]string{kafkaplat.TopicJobPosted},
		jobs.HandleJobPosted(jobs.NewGormMatchStore(gormDB), kafkaProducer, log))

	cacheInvalidatorConsumer := startConsumer(consumerCtx, &consumerWG, log, cacheInvalidatorConsumerGroup,
		[]string{kafkaplat.TopicSkillUpdated},
		jobs.HandleSkillUpdated(redisCache, log))

	shutdown.Wait(context.Background(), shutdown.Options{
		Logger:      log,
		GRPCServer:  grpcServer,
		HTTPServers: []*http.Server{httpServer},
		Cleanups: []shutdown.CleanupFunc{
			// Stop all three consumer goroutines first (cancel their
			// shared context, wait for Run to return) before closing
			// their underlying clients — this is what sends each
			// consumer group a clean LeaveGroup instead of leaving a
			// zombie member for the broker to time out, see
			// internal/platform/kafka.Consumer.Close's doc comment and
			// internal/platform/shutdown's for why this matters.
			func(_ context.Context) error {
				cancelConsumers()
				consumerWG.Wait()
				return nil
			},
			func(_ context.Context) error {
				snapshotConsumer.Close()
				matcherConsumer.Close()
				cacheInvalidatorConsumer.Close()
				return nil
			},
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
			func(_ context.Context) error {
				kafkaProducer.Close()
				return nil
			},
		},
	})
}

// startConsumer builds a Kafka consumer for groupID/topics and, if Kafka
// is configured on this instance, starts its Run loop in a new goroutine
// tracked by wg. Returns a non-nil *kafka.Consumer even when Kafka isn't
// configured (Close is always safe to call on it, matching the
// degrade-gracefully pattern used throughout this codebase) — the
// returned value is nil only if NewConsumerFromEnv fails for a reason
// other than "not configured," which is treated the same way: logged,
// this consumer just doesn't run.
func startConsumer(ctx context.Context, wg *sync.WaitGroup, log *zap.Logger, groupID string, topics []string, handler kafkaplat.Handler) *kafkaplat.Consumer {
	consumer, err := kafkaplat.NewConsumerFromEnv(os.Getenv, groupID, topics, log)
	if err != nil {
		log.Warn("kafka consumer not started; this projection/worker is disabled on this instance",
			zap.String("group_id", groupID), zap.Strings("topics", topics), zap.Error(err))
		return nil
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Info("kafka consumer starting", zap.String("group_id", groupID), zap.Strings("topics", topics))
		if runErr := consumer.Run(ctx, handler); runErr != nil {
			log.Error("kafka consumer stopped with error", zap.String("group_id", groupID), zap.Error(runErr))
		}
	}()
	return consumer
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
