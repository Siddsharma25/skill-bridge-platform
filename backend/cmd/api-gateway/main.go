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
	"google.golang.org/grpc/status"

	authv1 "github.com/Siddsharma25/skill-bridge-platform/backend/gen/auth/v1"
	jobsv1 "github.com/Siddsharma25/skill-bridge-platform/backend/gen/jobs/v1"
	skillsv1 "github.com/Siddsharma25/skill-bridge-platform/backend/gen/skills/v1"
	usersv1 "github.com/Siddsharma25/skill-bridge-platform/backend/gen/users/v1"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/gateway/authctx"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/gateway/cors"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/gateway/dataloader"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/gateway/graph"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/gateway/graph/generated"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/gateway/ratelimit"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/gateway/realtime"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/cache"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/health"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/jwks"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/logger"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/rabbitmq"
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

	// CORS (Phase 7): docs/SECURITY.md tracked "no CORS configuration
	// exists yet" as an open gap since the frontend didn't call the
	// backend at all until now. CORS_ALLOWED_ORIGINS is a comma-separated
	// explicit allowlist (never "*"), defaulting to Vite's local dev
	// origin — see internal/gateway/cors and docs/DECISIONS.md's Phase 7
	// notes for why an exact allowlist, not a wildcard, even though this
	// project's auth is a bearer token rather than a cookie.
	allowedOrigins := cors.LoadAllowedOriginsFromEnv(os.Getenv)
	log.Info("CORS allowed origins configured", zap.Strings("origins", allowedOrigins))

	authServiceAddr := envOr("AUTH_SERVICE_ADDR", "localhost:9001")
	authJWKSURL := envOr("AUTH_SERVICE_JWKS_URL", "http://localhost:8081/.well-known/jwks.json")
	skillsServiceAddr := envOr("SKILLS_SERVICE_ADDR", "localhost:9002")
	usersServiceAddr := envOr("USERS_SERVICE_ADDR", "localhost:9003")
	jobsServiceAddr := envOr("JOBS_SERVICE_ADDR", "localhost:9004")

	// Redis-backed rate limiting (Phase 1c). Same degrade-gracefully
	// pattern as skills-service/jobs-service's caching: an unset or
	// unreachable REDIS_URL means ratelimit.Middleware fails open (logs
	// and allows every request through) rather than the gateway failing
	// to start or rejecting traffic it can't actually count — see
	// internal/platform/cache and docs/DECISIONS.md.
	redisClient := cache.NewFromEnv(os.Getenv, log)
	rateLimitCfg := ratelimit.DefaultConfig
	if v := os.Getenv("RATE_LIMIT_REQUESTS"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			rateLimitCfg.Limit = n
		} else {
			log.Warn("ignoring invalid RATE_LIMIT_REQUESTS; using default", zap.String("value", v))
		}
	}
	if v := os.Getenv("RATE_LIMIT_WINDOW_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			rateLimitCfg.Window = time.Duration(n) * time.Second
		} else {
			log.Warn("ignoring invalid RATE_LIMIT_WINDOW_SECONDS; using default", zap.String("value", v))
		}
	}

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
		// Realtime backs the onNotification subscription (Phase 3.5) —
		// see internal/gateway/graph/schema.resolvers.go and
		// internal/gateway/realtime. Reuses the same Redis connection
		// rate limiting already established above; a disabled redisClient
		// (REDIS_URL unset/unreachable) just means the subscription
		// reports "unavailable" instead of streaming, same
		// degrade-gracefully convention as everywhere else.
		Realtime: redisClient,
	}
	// handler.New (not the deprecated NewDefaultServer) is used
	// specifically so the WebSocket transport can be configured with
	// wsInitFunc below — NewDefaultServer already registers its own
	// unauthenticated transport.Websocket{}, and graphql.Transport
	// selection picks the *first* transport whose Supports(r) matches
	// (see gqlgen's Server.getTransport), so an unauthenticated one added
	// first would shadow ours forever. This block otherwise mirrors
	// NewDefaultServer's body exactly (same cache sizes, same
	// extensions) — see that function's implementation in
	// github.com/99designs/gqlgen/graphql/handler for reference.
	srv := handler.New(generated.NewExecutableSchema(generated.Config{Resolvers: resolver}))
	srv.AddTransport(transport.Websocket{
		// InitFunc runs once per WebSocket connection, on that
		// connection's graphql-ws `connection_init` message — see
		// wsInitFunc's doc comment and docs/DECISIONS.md's Phase 3.5
		// notes for why WS auth can't reuse authctx.Middleware's
		// Authorization-header path.
		InitFunc:              wsInitFunc(jwksClient, log),
		KeepAlivePingInterval: 10 * time.Second,
		// gqlgen's default WebsocketImplementation (coder/websocket) has
		// its own Origin check independent of cors.Middleware above (that
		// middleware only ever runs on the HTTP request that *becomes* the
		// WS connection — coder/websocket's Accept does its own check
		// before handing back to gqlgen at all). Left at its zero value,
		// OriginPatterns is empty and coder/websocket rejects every
		// cross-origin upgrade outright (same-origin only), which is why
		// a real browser's `onNotification` subscription from Vite's dev
		// origin failed with "Origin ... is not authorized for Host ..."
		// while every plain HTTP query/mutation from that same origin
		// worked fine through cors.Middleware. Reusing allowedOrigins here
		// (each entry already scheme://host, matching what
		// authenticateOrigin compares against when a pattern contains
		// "://") keeps one source of truth for "which frontend origins
		// this gateway trusts" instead of a second hardcoded list.
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
	// Composition order (outermost to innermost), each wrapping the next:
	//
	//  1. authctx.Middleware — extracts and verifies a bearer token (if
	//     any) first, so both ratelimit.Middleware (keying by user ID when
	//     available) and every resolver's requireUserID check can read the
	//     verified caller from context. Never rejects here — see its own
	//     doc comment.
	//  2. ratelimit.Middleware — enforces the fixed-window budget. Must run
	//     after authctx so it can see the verified user ID, and before the
	//     GraphQL handler so a rejected request never reaches gqlgen at
	//     all. See internal/gateway/ratelimit and docs/DECISIONS.md.
	//  3. dataloader.Middleware — attaches a fresh per-request Loaders to
	//     context, batching every skill_id the response's resolvers need
	//     into one skills-service call (see internal/gateway/dataloader
	//     and Job.requiredSkills/Profile.skills/UserSkill.skill in
	//     schema.resolvers.go).
	//  4. srv — the actual GraphQL execution.
	mux.Handle("/query", authctx.Middleware(jwksClient, log)(
		ratelimit.Middleware(redisClient, rateLimitCfg, log)(
			dataloader.Middleware(skillsClient, usersClient)(srv),
		),
	))

	// Phase 3.5: api-gateway's first RabbitMQ consumer (every earlier use
	// of RabbitMQ in this codebase was a producer — auth-service
	// publishing notifications.email). Consumes notifications.realtime
	// (published by auth-service's Register and jobs-service's matching
	// worker — see internal/platform/rabbitmq) and republishes each
	// message onto Redis pub/sub via internal/gateway/realtime.Bridge, for
	// whichever gateway replica holds the matching user's live
	// onNotification subscription to pick up. Degrades gracefully, same
	// pattern as every other optional dependency: a missing/unreachable
	// RABBITMQ_URL just means this instance doesn't relay realtime
	// notifications, logged clearly, not a startup failure.
	realtimeBridge := realtime.NewBridge(redisClient, log)
	realtimeConsumerCtx, cancelRealtimeConsumer := context.WithCancel(context.Background())
	var realtimeConsumerWG sync.WaitGroup
	realtimeConsumer, err := rabbitmq.NewConsumerFromEnv(os.Getenv, rabbitmq.QueueNotificationsRealtime, log)
	if err != nil {
		log.Warn("rabbitmq realtime consumer not started; onNotification will not receive live pushes on this instance",
			zap.Error(err))
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

	httpServer := &http.Server{
		Addr: ":" + httpPort,
		// cors.Middleware wraps the whole mux (health checks included, not
		// just /query) — it's a no-op for any request with no Origin
		// header (same-origin, curl, grpcurl-style debugging), so wrapping
		// broadly costs nothing and means a future route added to mux
		// doesn't need to remember to opt in separately.
		Handler:           cors.Middleware(allowedOrigins, log)(mux),
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
			// Stop the realtime consumer goroutine first (cancel its
			// context, wait for Run to return) before closing its
			// underlying connection — same "stop accepting work before
			// tearing down what it depends on" ordering as
			// cmd/jobs-service/main.go's Kafka consumer shutdown, and what
			// avoids a zombie in-flight message getting nacked into a
			// closed channel.
			//
			// Note what this does NOT do: an open onNotification
			// WebSocket connection is not itself told to close here.
			// net/http's own Shutdown doc comment is explicit that it
			// "does not attempt to close nor wait for hijacked
			// connections such as WebSockets" — httpServer.Shutdown
			// (called just above, before these Cleanups run) returns
			// without cancelling that connection's request context, so a
			// live subscription's forwarding goroutine
			// (schema.resolvers.go's OnNotification) keeps running until
			// either the client disconnects on its own or this process
			// actually exits (which severs every remaining connection
			// regardless). This is a known, accepted gap — see
			// docs/DECISIONS.md's Phase 3.5 notes — consistent with this
			// codebase's existing "no reconnect/graceful-notify
			// machinery" stance elsewhere.
			//
			// What DOES need bounding here, discovered live (see
			// docs/DECISIONS.md): Consumer.Close's underlying
			// amqp091-go Channel.Close call can itself hang indefinitely
			// in some circumstances — internal/platform/rabbitmq's
			// closeTimeout is what guarantees this step (and therefore
			// this whole shutdown sequence) makes bounded progress
			// regardless.
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
			func(_ context.Context) error {
				return redisClient.Close()
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

// closeCodeUnauthorized is the graphql-ws protocol's own recommended
// WebSocket close code for a connection_init that fails authentication —
// see wsInitFunc.
const closeCodeUnauthorized = 4401

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// wsInitFunc authenticates a WebSocket connection at graphql-ws's
// `connection_init` step (Phase 3.5) — the WebSocket-transport
// counterpart to authctx.Middleware's HTTP `Authorization: Bearer <token>`
// header parsing above. A WebSocket upgrade request doesn't naturally
// carry a bearer header on every subsequent message the way HTTP does, so
// the graphql-ws protocol instead has the client send auth once, in the
// `connection_init` message's payload; gqlgen's transport.Websocket calls
// this exactly once per connection, before any `subscribe` message on
// that connection is accepted.
//
// Accepts the token under either "Authorization" (optionally prefixed
// "Bearer ", matching the HTTP convention most graphql-ws clients — the
// npm graphql-ws library, Apollo, urql — already follow for their
// connectionParams) or a bare "token" key, for a client that would rather
// not spoof an HTTP-style header inside a JSON payload.
//
// Unlike authctx.Middleware — which never rejects an HTTP request itself,
// leaving that to each resolver's requireUserID, since /query serves both
// authenticated and unauthenticated operations — a WebSocket connection
// with no valid token is rejected here outright: a non-nil error return
// makes gqlgen close the socket rather than send a connection_ack (see
// gqlgen's wsConnection.init). Unlike /query, every use of this WebSocket
// endpoint today is the onNotification subscription, which always
// requires an authenticated caller, so there is no unrelated
// "unauthenticated but otherwise valid" case to preserve here the way
// there is for a mixed HTTP endpoint.
//
// closeCodeUnauthorized (4401, the graphql-ws protocol's own recommended
// "Unauthorized" code — see
// https://github.com/enisdenjo/graphql-ws/blob/master/PROTOCOL.md) is set
// on the returned context via transport.WithWebsocketCloseCode so a real
// client can distinguish "you rejected my auth" from an ordinary closure.
// Note this only reaches the wire under the modern `graphql-transport-ws`
// subprotocol's raw WebSocket close frame; under the legacy `graphql-ws`
// subprotocol gqlgen also sends a connection_error message first (see
// graphqlwsMessageExchanger.fromMessage vs.
// graphqltransportwsMessageExchanger.fromMessage, where the latter treats
// connectionErrorMessageType as a no-op and relies on the close code
// alone) — either way, a rejected connection never reaches connection_ack.
//
// The context returned becomes this connection's base context for every
// subscription made over it (see gqlgen's wsConnection.ctx) — a single
// wsConnection struct exists per accepted WebSocket, so this verified
// identity can never leak into a different connection's context; each
// connection gets its own InitFunc call and its own derived context.
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
