// Command allinone is Phase 7's production deploy target: one process
// registering all four backend services' gRPC servers (auth, skills,
// users, jobs) on loopback-only addresses, plus api-gateway's HTTP/
// GraphQL/WebSocket server as the single public-facing listener — the
// shape docs/DECISIONS.md's "Production deployment: a single allinone
// binary, not 5 live services" section already committed to before this
// binary existed. See that section (and this phase's own notes, appended
// alongside it) for the full reasoning; the short version:
//
// Render's free tier is realistically one web service (background workers
// are paid-only, free services spin down after ~15 min idle). Running the
// full five-service mesh live for $0 isn't realistic, and a gateway
// fanning out to four *separately* sleeping services would serially
// cold-start all of them on one request. Registering all four services'
// construction code in one process — the exact same auth.NewServer /
// skills.NewServer / users.NewServer / jobs.NewServer / gateway resolver
// wiring every cmd/<service>/main.go already uses — collapses that to one
// cold start, with zero public exposure of the four backend gRPC ports
// (they bind 127.0.0.1 only; nothing outside this process can reach them
// directly, not even another process on the same machine's other network
// interfaces).
//
// This file is assembly, not new business logic: every NewServer call,
// every gRPC/HTTP wiring pattern, and the gateway's resolver/client wiring
// below is copied unchanged from cmd/auth-service, cmd/skills-service,
// cmd/users-service, cmd/jobs-service, and cmd/api-gateway respectively —
// see each for the "why" behind its own piece. What's genuinely new here:
//
//   - Fixed loopback addresses for the four backend services (below) — no
//     service discovery needed, since it's all one process. Not
//     overridable via PORT/HTTP_PORT the way each service's own main.go
//     is, because PORT here is reserved for the gateway's public port
//     (Render's convention — see envOr("PORT", ...) below) and there's no
//     second process to disambiguate against.
//   - Per-service DATABASE_URL scoping (dbGetenv) — four services still
//     need four distinct scoped-role connection strings despite sharing
//     one process-wide environment; reuses the AUTH_DATABASE_URL /
//     SKILLS_DATABASE_URL / USERS_DATABASE_URL / JOBS_DATABASE_URL naming
//     docker/docker-compose.yml already established in Phase 4, rather
//     than inventing a new convention.
//   - One shutdown.Wait call covering all four gRPC servers (via the new
//     GRPCServers field — see internal/platform/shutdown) plus the
//     gateway's public HTTP server plus every service's own loopback
//     health/JWKS HTTP server, in one graceful sequence.
//
// What's explicitly NOT here: notification-service (NestJS). Per the
// plan, Kafka/RabbitMQ async flows — job matching via job.posted /
// user.skills.updated, the RabbitMQ welcome-email queue, realtime
// WebSocket push notifications — simply don't exist in production. This
// binary still constructs every Kafka/RabbitMQ producer/consumer exactly
// as each original service does (KAFKA_BROKERS/RABBITMQ_URL are read the
// same way, degrading gracefully to "disabled" when unset — see
// internal/platform/kafka and internal/platform/rabbitmq), so nothing
// here special-cases "production": render.yaml simply never sets those
// two env vars. The consequence, stated plainly: jobs.job_matches is
// never populated in production (nothing publishes/consumes job.posted or
// user.skills.updated without Kafka), no welcome-email log line is ever
// recorded, and onNotification never receives a live push. Register,
// login, createSkill, createJob, and myProfile — the synchronous GraphQL
// surface — all work identically to the full microservices deployment,
// since none of them depend on a broker. See docs/DECISIONS.md's Phase 7
// notes for the full accounting of what this trims versus the local/kind
// deployment.
package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/lru"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/99designs/gqlgen/graphql/playground"
	coderws "github.com/coder/websocket"
	"github.com/joho/godotenv"
	"github.com/vektah/gqlparser/v2/ast"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	authv1 "github.com/Siddsharma25/skill-bridge-platform/backend/gen/auth/v1"
	jobsv1 "github.com/Siddsharma25/skill-bridge-platform/backend/gen/jobs/v1"
	skillsv1 "github.com/Siddsharma25/skill-bridge-platform/backend/gen/skills/v1"
	usersv1 "github.com/Siddsharma25/skill-bridge-platform/backend/gen/users/v1"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/auth"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/gateway/authctx"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/gateway/cors"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/gateway/dataloader"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/gateway/graph"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/gateway/graph/generated"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/gateway/ratelimit"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/gateway/realtime"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/jobs"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/cache"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/db"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/health"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/jwks"
	kafkaplat "github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/kafka"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/logger"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/rabbitmq"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/requestid"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/shutdown"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/skills"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/users"
)

// Fixed loopback addresses for the four backend services. Not
// configurable via env — this binary owns all four at once, there's no
// second process to point at a different address, and "loopback only,
// nothing external ever reaches them" is the entire point (see the
// package doc comment). Numbers match each service's own dev-default port
// (see backend/README.md's ports table) purely so log lines look
// familiar across the two deployment shapes, not because anything here
// depends on the specific values.
const (
	authGRPCAddr   = "127.0.0.1:9001"
	authHTTPAddr   = "127.0.0.1:8081"
	skillsGRPCAddr = "127.0.0.1:9002"
	skillsHTTPAddr = "127.0.0.1:8082"
	usersGRPCAddr  = "127.0.0.1:9003"
	usersHTTPAddr  = "127.0.0.1:8083"
	jobsGRPCAddr   = "127.0.0.1:9004"
	jobsHTTPAddr   = "127.0.0.1:8084"

	authJWKSURL = "http://" + authHTTPAddr + "/.well-known/jwks.json"
)

// Kafka consumer group IDs, identical to cmd/jobs-service/main.go's — see
// that file's own doc comment for why each responsibility (snapshot
// projection, matching worker, cache invalidation) gets its own group.
// Kept identical (not "allinone."-prefixed) since this is the same
// logical jobs-service consumer, just embedded in a different binary; if
// KAFKA_BROKERS is ever pointed at the same cluster a standalone
// jobs-service is also consuming from, the two would correctly share
// partitions as one scaled-out group rather than double-processing every
// event.
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

	log, err := logger.New("allinone")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to build logger: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = log.Sync() }()

	// ==================================================================
	// auth-service — identical construction to cmd/auth-service/main.go,
	// see that file for the why behind each piece.
	// ==================================================================
	authDB := connectDB(log, "auth-service", "AUTH")

	keyPair, err := jwks.LoadOrGenerate(os.Getenv)
	if err != nil {
		log.Fatal("failed to load or generate JWT keypair", zap.Error(err))
	}
	if os.Getenv("JWT_PRIVATE_KEY_PEM") == "" {
		log.Warn("JWT_PRIVATE_KEY_PEM not set; generated an ephemeral RS256 keypair for this process only " +
			"(fine for local dev, NOT fine for Render — a restart invalidates every outstanding token). " +
			"Set JWT_PRIVATE_KEY_PEM explicitly in render.yaml's env vars.")
	}
	issuer := envOr("JWT_ISSUER", "skill-bridge-platform/auth-service")

	googleExchanger, googleConfigured := auth.NewGoogleExchangerFromEnv(os.Getenv)
	if !googleConfigured {
		log.Warn("starting without Google OAuth configured; GetGoogleAuthURL/GoogleOAuthCallback will return FailedPrecondition")
	} else {
		log.Info("Google OAuth configured")
	}

	authRabbitProducer := rabbitmq.NewProducerFromEnv(os.Getenv, log)

	authServer := auth.NewServer(authDB, keyPair, issuer, googleExchanger, authRabbitProducer, log)

	authGRPCServer := grpc.NewServer(grpc.ChainUnaryInterceptor(requestid.UnaryServerInterceptor()))
	authv1.RegisterAuthServiceServer(authGRPCServer, authServer)
	health.NewGRPCServer(authGRPCServer)
	reflection.Register(authGRPCServer)
	authLis := mustListen(log, "auth-service", authGRPCAddr)
	go serveGRPC(log, "auth-service", authGRPCServer, authLis)

	authMux := health.Mux(readyCheckDB(authDB))
	authMux.HandleFunc("/.well-known/jwks.json", jwks.Handler(keyPair))
	authHTTPServer := &http.Server{Addr: authHTTPAddr, Handler: authMux, ReadHeaderTimeout: 5 * time.Second}
	go serveHTTP(log, "auth-service", authHTTPServer)

	// ==================================================================
	// skills-service — identical construction to cmd/skills-service/main.go.
	// ==================================================================
	skillsDB := connectDB(log, "skills-service", "SKILLS")
	skillsCache := cache.NewFromEnv(os.Getenv, log)
	skillsKafkaProducer := kafkaplat.NewProducerFromEnv(os.Getenv, log)

	skillsServer := skills.NewServer(skillsDB, skillsCache, skillsKafkaProducer, log)

	skillsGRPCServer := grpc.NewServer(grpc.ChainUnaryInterceptor(requestid.UnaryServerInterceptor()))
	skillsv1.RegisterSkillsServiceServer(skillsGRPCServer, skillsServer)
	health.NewGRPCServer(skillsGRPCServer)
	reflection.Register(skillsGRPCServer)
	skillsLis := mustListen(log, "skills-service", skillsGRPCAddr)
	go serveGRPC(log, "skills-service", skillsGRPCServer, skillsLis)

	skillsMux := health.Mux(readyCheckDB(skillsDB))
	skillsHTTPServer := &http.Server{Addr: skillsHTTPAddr, Handler: skillsMux, ReadHeaderTimeout: 5 * time.Second}
	go serveHTTP(log, "skills-service", skillsHTTPServer)

	// ==================================================================
	// users-service — identical construction to cmd/users-service/main.go.
	// ==================================================================
	usersDB := connectDB(log, "users-service", "USERS")
	usersKafkaProducer := kafkaplat.NewProducerFromEnv(os.Getenv, log)

	usersServer := users.NewServer(usersDB, usersKafkaProducer, log)

	usersGRPCServer := grpc.NewServer(grpc.ChainUnaryInterceptor(requestid.UnaryServerInterceptor()))
	usersv1.RegisterUsersServiceServer(usersGRPCServer, usersServer)
	health.NewGRPCServer(usersGRPCServer)
	reflection.Register(usersGRPCServer)
	usersLis := mustListen(log, "users-service", usersGRPCAddr)
	go serveGRPC(log, "users-service", usersGRPCServer, usersLis)

	usersMux := health.Mux(readyCheckDB(usersDB))
	usersHTTPServer := &http.Server{Addr: usersHTTPAddr, Handler: usersMux, ReadHeaderTimeout: 5 * time.Second}
	go serveHTTP(log, "users-service", usersHTTPServer)

	// ==================================================================
	// jobs-service — identical construction to cmd/jobs-service/main.go,
	// including its three Kafka consumers (snapshot projection, matching
	// worker, cache invalidation). All three degrade gracefully exactly
	// like every other optional dependency here: if KAFKA_BROKERS isn't
	// set (the expected case in production — see the package doc comment),
	// each one just doesn't run on this instance, logged clearly.
	// ==================================================================
	jobsDB := connectDB(log, "jobs-service", "JOBS")
	jobsCache := cache.NewFromEnv(os.Getenv, log)
	jobsKafkaProducer := kafkaplat.NewProducerFromEnv(os.Getenv, log)
	jobsRabbitProducer := rabbitmq.NewProducerFromEnv(os.Getenv, log)

	jobsServer := jobs.NewServer(jobsDB, jobsCache, jobsKafkaProducer, log)

	jobsGRPCServer := grpc.NewServer(grpc.ChainUnaryInterceptor(requestid.UnaryServerInterceptor()))
	jobsv1.RegisterJobsServiceServer(jobsGRPCServer, jobsServer)
	health.NewGRPCServer(jobsGRPCServer)
	reflection.Register(jobsGRPCServer)
	jobsLis := mustListen(log, "jobs-service", jobsGRPCAddr)
	go serveGRPC(log, "jobs-service", jobsGRPCServer, jobsLis)

	jobsMux := health.Mux(readyCheckDB(jobsDB))
	jobsHTTPServer := &http.Server{Addr: jobsHTTPAddr, Handler: jobsMux, ReadHeaderTimeout: 5 * time.Second}
	go serveHTTP(log, "jobs-service", jobsHTTPServer)

	consumerCtx, cancelConsumers := context.WithCancel(context.Background())
	var consumerWG sync.WaitGroup
	snapshotConsumer := startConsumer(consumerCtx, &consumerWG, log, snapshotConsumerGroup,
		[]string{kafkaplat.TopicUserSkillsUpdated},
		jobs.HandleUserSkillsUpdated(jobs.NewGormSnapshotStore(jobsDB), log))
	matcherConsumer := startConsumer(consumerCtx, &consumerWG, log, matcherConsumerGroup,
		[]string{kafkaplat.TopicJobPosted},
		jobs.HandleJobPosted(jobs.NewGormMatchStore(jobsDB), jobsKafkaProducer, jobsRabbitProducer, log))
	cacheInvalidatorConsumer := startConsumer(consumerCtx, &consumerWG, log, cacheInvalidatorConsumerGroup,
		[]string{kafkaplat.TopicSkillUpdated},
		jobs.HandleSkillUpdated(jobsCache, log))

	// ==================================================================
	// api-gateway — identical construction to cmd/api-gateway/main.go,
	// except the four gRPC client connections dial the fixed loopback
	// addresses above directly instead of env-configurable
	// AUTH_SERVICE_ADDR/SKILLS_SERVICE_ADDR/USERS_SERVICE_ADDR/
	// JOBS_SERVICE_ADDR — no service discovery needed, it's all one
	// process (see the package doc comment).
	// ==================================================================
	publicPort := envOr("PORT", "8080") // Render sets PORT; 8080 matches every other phase's local dev default.

	allowedOrigins := cors.LoadAllowedOriginsFromEnv(os.Getenv)
	log.Info("CORS allowed origins configured", zap.Strings("origins", allowedOrigins))

	gatewayRedis := cache.NewFromEnv(os.Getenv, log)
	rateLimitCfg := ratelimit.DefaultConfig
	if v := os.Getenv("RATE_LIMIT_REQUESTS"); v != "" {
		if n, perr := strconv.ParseInt(v, 10, 64); perr == nil && n > 0 {
			rateLimitCfg.Limit = n
		} else {
			log.Warn("ignoring invalid RATE_LIMIT_REQUESTS; using default", zap.String("value", v))
		}
	}
	if v := os.Getenv("RATE_LIMIT_WINDOW_SECONDS"); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil && n > 0 {
			rateLimitCfg.Window = time.Duration(n) * time.Second
		} else {
			log.Warn("ignoring invalid RATE_LIMIT_WINDOW_SECONDS; using default", zap.String("value", v))
		}
	}

	authConn := dialLoopback(log, "auth-service", authGRPCAddr, requestid.UnaryClientInterceptor())
	authClient := authv1.NewAuthServiceClient(authConn)

	skillsConn := dialLoopback(log, "skills-service", skillsGRPCAddr, requestid.UnaryClientInterceptor())
	skillsClient := skillsv1.NewSkillsServiceClient(skillsConn)

	jobsConn := dialLoopback(log, "jobs-service", jobsGRPCAddr, requestid.UnaryClientInterceptor())
	jobsClient := jobsv1.NewJobsServiceClient(jobsConn)

	usersConn := dialLoopback(log, "users-service", usersGRPCAddr,
		requestid.UnaryClientInterceptor(), authctx.UnaryClientInterceptor())
	usersClient := usersv1.NewUsersServiceClient(usersConn)

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
		Realtime:     gatewayRedis,
	}
	srv := handler.New(generated.NewExecutableSchema(generated.Config{Resolvers: resolver}))
	srv.AddTransport(transport.Websocket{
		InitFunc:              wsInitFunc(jwksClient, log),
		KeepAlivePingInterval: 10 * time.Second,
		// See api-gateway/main.go's identical Implementation field for
		// why this is required: gqlgen's default WebsocketImplementation
		// (coder/websocket) has its own same-origin-only Origin check,
		// independent of cors.Middleware, that rejects every real
		// deployed frontend's onNotification subscription unless told
		// which origins to trust.
		Implementation: transport.CoderWebsocketImplementation{
			AcceptOptions: coderws.AcceptOptions{OriginPatterns: allowedOrigins},
		},
	})
	srv.AddTransport(transport.Options{})
	srv.AddTransport(transport.GET{})
	srv.AddTransport(transport.POST{})
	srv.AddTransport(transport.MultipartForm{})
	srv.SetQueryCache(lru.New[*ast.QueryDocument](1000))
	srv.Use(extension.Introspection{})
	srv.Use(extension.AutomaticPersistedQuery{Cache: lru.New[string](100)})

	mux := health.Mux(func(ctx context.Context) error {
		// Same readiness definition cmd/api-gateway/main.go uses: "can I
		// still reach auth-service" — the one hard dependency the
		// gateway's own HTTP surface has. This is what Render's health
		// check (item 3 of this phase's brief) actually polls.
		pingCtx, pingCancel := context.WithTimeout(ctx, 2*time.Second)
		defer pingCancel()
		_, err := authClient.Login(pingCtx, &authv1.LoginRequest{Email: "", Password: ""})
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
	mux.Handle("/query", authctx.Middleware(jwksClient, log)(
		ratelimit.Middleware(gatewayRedis, rateLimitCfg, log)(
			dataloader.Middleware(skillsClient, usersClient)(srv),
		),
	))

	realtimeBridge := realtime.NewBridge(gatewayRedis, log)
	realtimeConsumerCtx, cancelRealtimeConsumer := context.WithCancel(context.Background())
	var realtimeConsumerWG sync.WaitGroup
	realtimeConsumer, rtErr := rabbitmq.NewConsumerFromEnv(os.Getenv, rabbitmq.QueueNotificationsRealtime, log)
	if rtErr != nil {
		log.Warn("rabbitmq realtime consumer not started; onNotification will not receive live pushes on this instance",
			zap.Error(rtErr))
	} else {
		realtimeConsumerWG.Add(1)
		go func() {
			defer realtimeConsumerWG.Done()
			log.Info("rabbitmq realtime consumer starting", zap.String("queue", rabbitmq.QueueNotificationsRealtime))
			if runErr := realtimeConsumer.Run(realtimeConsumerCtx, realtimeBridge.Handler()); runErr != nil {
				log.Error("rabbitmq realtime consumer stopped with error", zap.Error(runErr))
			}
		}()
	}

	publicHTTPServer := &http.Server{
		Addr:              ":" + publicPort,
		Handler:           cors.Middleware(allowedOrigins, log)(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Info("allinone public HTTP server listening", zap.String("port", publicPort))
		if err := publicHTTPServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("public HTTP server stopped unexpectedly", zap.Error(err))
		}
	}()

	// ==================================================================
	// One graceful shutdown sequence for the whole process: all four
	// backend gRPC servers (concurrently, via shutdown.Options.GRPCServers
	// — see internal/platform/shutdown), every HTTP server (the public
	// gateway one plus each service's own loopback health/JWKS one), then
	// cleanups in dependency order (stop consumers before closing what
	// they publish/read through, close DB/cache/broker connections last).
	// ==================================================================
	shutdown.Wait(context.Background(), shutdown.Options{
		Logger: log,
		GRPCServers: []*grpc.Server{
			authGRPCServer, skillsGRPCServer, usersGRPCServer, jobsGRPCServer,
		},
		HTTPServers: []*http.Server{
			publicHTTPServer, authHTTPServer, skillsHTTPServer, usersHTTPServer, jobsHTTPServer,
		},
		Cleanups: []shutdown.CleanupFunc{
			// Stop the realtime consumer goroutine and jobs-service's
			// three Kafka consumer goroutines first (cancel their
			// contexts, wait for Run to return) before closing their
			// underlying clients — same ordering cmd/api-gateway and
			// cmd/jobs-service's own mains use, for the same reason
			// (a clean LeaveGroup / unsubscribe, not a zombie member).
			func(_ context.Context) error {
				cancelRealtimeConsumer()
				realtimeConsumerWG.Wait()
				return nil
			},
			func(_ context.Context) error {
				realtimeConsumer.Close()
				return nil
			},
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
			func(_ context.Context) error { return authConn.Close() },
			func(_ context.Context) error { return skillsConn.Close() },
			func(_ context.Context) error { return usersConn.Close() },
			func(_ context.Context) error { return jobsConn.Close() },
			func(_ context.Context) error { return closeDB(authDB) },
			func(_ context.Context) error { return closeDB(skillsDB) },
			func(_ context.Context) error { return closeDB(usersDB) },
			func(_ context.Context) error { return closeDB(jobsDB) },
			func(_ context.Context) error { return gatewayRedis.Close() },
			func(_ context.Context) error { return skillsCache.Close() },
			func(_ context.Context) error { return jobsCache.Close() },
			func(_ context.Context) error { authRabbitProducer.Close(); return nil },
			func(_ context.Context) error { jobsRabbitProducer.Close(); return nil },
			func(_ context.Context) error { skillsKafkaProducer.Close(); return nil },
			func(_ context.Context) error { usersKafkaProducer.Close(); return nil },
			func(_ context.Context) error { jobsKafkaProducer.Close(); return nil },
		},
	})
}

// dbGetenv returns a getenv function that maps the single key
// "DATABASE_URL" to "<prefix>_DATABASE_URL" and passes everything else
// through to os.Getenv unchanged. db.LoadConfig (internal/platform/db) is
// already parameterized to take a getenv function for exactly this
// reason (see its own doc comment); this is what lets four services share
// one process-wide environment while each still getting its own scoped
// Postgres role's connection string. Matches the naming convention
// docker/docker-compose.yml established in Phase 4 (AUTH_DATABASE_URL,
// SKILLS_DATABASE_URL, USERS_DATABASE_URL, JOBS_DATABASE_URL) rather than
// inventing a new one — see render.yaml, which sets these same four names.
func dbGetenv(prefix string) func(string) string {
	return func(key string) string {
		if key == "DATABASE_URL" {
			return os.Getenv(prefix + "_DATABASE_URL")
		}
		return os.Getenv(key)
	}
}

// connectDB connects serviceName's own scoped database, degrading
// gracefully exactly like every cmd/<service>/main.go does: a missing or
// unreachable connection string logs a warning and leaves the returned
// *gorm.DB nil rather than failing the whole process to start. envPrefix
// is one of AUTH/SKILLS/USERS/JOBS (see dbGetenv).
func connectDB(log *zap.Logger, serviceName, envPrefix string) *gorm.DB {
	cfg := db.LoadConfig(dbGetenv(envPrefix))
	conn, err := db.Connect(cfg)
	if err != nil {
		log.Warn(serviceName+": starting without a database connection; RPCs needing it will return Unavailable until "+
			envPrefix+"_DATABASE_URL is set and reachable", zap.Error(err))
		return nil
	}
	log.Info(serviceName + ": connected to database")
	return conn
}

// closeDB is the cleanup step every cmd/<service>/main.go already uses
// for its own *gorm.DB — a no-op if the connection was never established.
func closeDB(gormDB *gorm.DB) error {
	if gormDB == nil {
		return nil
	}
	sqlDB, err := gormDB.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// readyCheckDB builds a health.ReadyCheckFunc identical to the one every
// cmd/<service>/main.go registers on its own HTTP mux: "not ready" if the
// database isn't configured or isn't reachable.
func readyCheckDB(gormDB *gorm.DB) health.ReadyCheckFunc {
	return func(_ context.Context) error {
		if gormDB == nil {
			return fmt.Errorf("database is not configured")
		}
		return db.Ping(gormDB)
	}
}

// mustListen opens a TCP listener for one of the four fixed loopback
// addresses, exiting the whole process on failure — identical severity to
// every cmd/<service>/main.go's own log.Fatal on a listen failure (a
// service that can't bind its port at all isn't something to silently
// skip, unlike a missing DATABASE_URL).
func mustListen(log *zap.Logger, serviceName, addr string) net.Listener {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal(serviceName+": failed to listen for gRPC", zap.String("addr", addr), zap.Error(err))
	}
	return lis
}

// serveGRPC runs one backend service's gRPC server until it stops (via
// shutdown.Wait's GracefulStop), logging unexpected exits — identical
// shape to every cmd/<service>/main.go's own serve goroutine.
func serveGRPC(log *zap.Logger, serviceName string, srv *grpc.Server, lis net.Listener) {
	log.Info(serviceName+": gRPC server listening", zap.String("addr", lis.Addr().String()))
	if err := srv.Serve(lis); err != nil && err != grpc.ErrServerStopped {
		log.Error(serviceName+": gRPC server stopped unexpectedly", zap.Error(err))
	}
}

// serveHTTP runs one backend service's loopback health/JWKS HTTP server
// until it stops (via shutdown.Wait's Shutdown), logging unexpected exits.
func serveHTTP(log *zap.Logger, serviceName string, srv *http.Server) {
	log.Info(serviceName+": HTTP server listening (health"+jwksSuffix(serviceName)+")", zap.String("addr", srv.Addr))
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Error(serviceName+": HTTP server stopped unexpectedly", zap.Error(err))
	}
}

func jwksSuffix(serviceName string) string {
	if serviceName == "auth-service" {
		return " + JWKS"
	}
	return ""
}

// dialLoopback builds a gRPC client connection to one of the four fixed
// loopback addresses, with the same request-ID (and, for users-service,
// verified-user-ID) client interceptors cmd/api-gateway/main.go's own
// client construction uses. grpc.NewClient connects lazily — the gateway
// half of this process starts serving even if a backend service's own
// startup is still in progress, and gRPC transparently retries per-call
// (there's no ordering dependency to get right between the goroutines
// started above).
func dialLoopback(log *zap.Logger, serviceName, addr string, interceptors ...grpc.UnaryClientInterceptor) *grpc.ClientConn {
	conn, err := grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(interceptors...),
	)
	if err != nil {
		log.Fatal("failed to create "+serviceName+" gRPC client", zap.String("addr", addr), zap.Error(err))
	}
	return conn
}

// startConsumer is identical to cmd/jobs-service/main.go's helper of the
// same name — see that file's doc comment.
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

// isTransportError and wsInitFunc below are copied verbatim from
// cmd/api-gateway/main.go (unexported helpers, so importing them directly
// isn't an option across two independent `package main` binaries) — see
// that file for the full doc comments on the WebSocket-auth reasoning
// (connection_init vs. an Authorization header, the 4401 close code,
// per-connection context isolation, etc.). Keep these two in sync with
// cmd/api-gateway/main.go's copies if that file's WebSocket-auth logic
// ever changes; nothing here diverges from it on purpose.

// isTransportError reports whether err looks like a connection-level
// failure (a backend service unreachable) rather than a well-formed gRPC
// business error (which itself proves the connection works).
func isTransportError(err error) bool {
	return status.Code(err) == codes.Unavailable
}

// closeCodeUnauthorized is the graphql-ws protocol's own recommended
// WebSocket close code for a connection_init that fails authentication.
const closeCodeUnauthorized = 4401

// wsInitFunc authenticates a WebSocket connection at graphql-ws's
// `connection_init` step — see cmd/api-gateway/main.go's copy of this
// function for the full reasoning.
func wsInitFunc(jwksClient *jwks.Client, log *zap.Logger) transport.WebsocketInitFunc {
	return func(ctx context.Context, initPayload transport.InitPayload) (context.Context, *transport.InitPayload, error) {
		raw := initPayload.Authorization()
		if raw == "" {
			raw = initPayload.GetString("token")
		}
		token := strings.TrimSpace(strings.TrimPrefix(raw, "Bearer "))
		if token == "" {
			return transport.WithWebsocketCloseCode(ctx, closeCodeUnauthorized), nil,
				fmt.Errorf("authentication required: connection_init payload must include an Authorization (or token) field")
		}

		userID, err := authctx.VerifyToken(ctx, jwksClient, token)
		if err != nil {
			log.Debug("rejecting websocket connection: invalid token", zap.Error(err))
			return transport.WithWebsocketCloseCode(ctx, closeCodeUnauthorized), nil,
				fmt.Errorf("authentication required: invalid token")
		}

		return authctx.NewContext(ctx, userID), nil, nil
	}
}
