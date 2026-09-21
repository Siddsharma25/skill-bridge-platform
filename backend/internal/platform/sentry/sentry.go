// Package sentry is this monorepo's one wrapper around Sentry error
// tracking and log capture. It exists to close a real gap the local
// Prometheus/Loki/Jaeger/Grafana stack (docker/docker-compose.observability.yml)
// structurally can't: that stack is local/kind-only, so production
// (cmd/allinone on Render) ships with zero error visibility today — if
// something breaks there, the only signal is a user report. See
// docs/DECISIONS.md's Sentry section for the full reasoning and
// backend/internal/platform/README.md for how this fits the other
// packages here.
//
// Same degrade-gracefully convention as cache/kafka/rabbitmq: a missing or
// invalid SENTRY_DSN disables error tracking for this instance, logged
// once, never a startup failure.
package sentry

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	sentrygo "github.com/getsentry/sentry-go"
	sentryhttp "github.com/getsentry/sentry-go/http"
	"github.com/TheZeroSlave/zapsentry"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Handle is the result of InitFromEnv — always safe to use, even when
// Sentry isn't configured (every method becomes a no-op / pass-through),
// same "return something usable, never nil-check at the call site" shape
// every other internal/platform package follows (cache.Client,
// kafka.Producer, rabbitmq.Producer, ...).
type Handle struct {
	enabled bool
}

// InitFromEnv initializes the global Sentry SDK from SENTRY_DSN (plus
// optional SENTRY_ENVIRONMENT, SENTRY_TRACES_SAMPLE_RATE). TracesSampleRate
// defaults to 0 (performance tracing off) rather than Sentry's own
// historical "unset means sample everything" default — this project would
// rather an operator opt in deliberately than silently burn the free
// tier's performance-unit quota the first time this ships.
func InitFromEnv(getenv func(string) string, service string, log *zap.Logger) *Handle {
	dsn := getenv("SENTRY_DSN")
	if dsn == "" {
		log.Info("SENTRY_DSN not set; Sentry error tracking and log capture are disabled on this instance")
		return &Handle{}
	}

	environment := getenv("SENTRY_ENVIRONMENT")
	if environment == "" {
		environment = "development"
	}

	tracesSampleRate := 0.0
	if v := getenv("SENTRY_TRACES_SAMPLE_RATE"); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil && n >= 0 && n <= 1 {
			tracesSampleRate = n
		} else {
			log.Warn("ignoring invalid SENTRY_TRACES_SAMPLE_RATE; using default (0, performance tracing disabled)",
				zap.String("value", v))
		}
	}

	err := sentrygo.Init(sentrygo.ClientOptions{
		Dsn:              dsn,
		Environment:      environment,
		ServerName:       service,
		TracesSampleRate: tracesSampleRate,
		AttachStacktrace: true,
		// Never capture HTTP request/response bodies. Verified by reading
		// sentry-go's own data_collection.go rather than assumed: headers,
		// cookies, and query params are already redacted by the SDK's
		// built-in denylist ("auth", "bearer", "token", "password", ...),
		// but HTTPBodies defaults to collecting *every* body type
		// verbatim. This app's register/login GraphQL mutations carry a
		// plaintext password in the POST body — an empty (not nil) slice
		// here is what actually disables body collection. See
		// docs/SECURITY.md.
		DataCollection: &sentrygo.DataCollection{
			HTTPBodies: []sentrygo.BodyType{},
		},
	})
	if err != nil {
		log.Error("failed to initialize Sentry; error tracking and log capture are disabled on this instance",
			zap.Error(err))
		return &Handle{}
	}

	log.Info("Sentry configured",
		zap.String("environment", environment), zap.Float64("traces_sample_rate", tracesSampleRate))
	return &Handle{enabled: true}
}

// Enabled reports whether this Handle has a live Sentry client.
func (h *Handle) Enabled() bool {
	return h != nil && h.enabled
}

// Flush blocks until buffered events are delivered or timeout elapses.
// Safe to call on a disabled Handle (a no-op) — every cmd/*/main.go defers
// this unconditionally, same as sqlDB.Close()/producer.Close() elsewhere.
func (h *Handle) Flush(timeout time.Duration) {
	if h.Enabled() {
		sentrygo.Flush(timeout)
	}
}

// WrapCore attaches a Sentry-reporting zapcore.Core alongside base, so
// every zap.Error/zap.Fatal call already in this codebase also reports to
// Sentry — the "keeping logs" half of this package's job — without
// touching any of those call sites individually. Safe to call on a
// disabled Handle: base is returned unchanged. Intended use:
//
//	log = log.WithOptions(zap.WrapCore(sentryHandle.WrapCore))
//
// Only Error level and above is forwarded as a Sentry event (Info/Debug
// become breadcrumbs attached to whatever error/event follows them,
// giving Sentry's issue view a few lines of "what happened right before
// this" for free) — this deliberately mirrors what already counts as
// noteworthy in this codebase's own logging convention (see
// backend/CLAUDE.md's error-handling pattern: "log the error where it's
// handled").
func (h *Handle) WrapCore(base zapcore.Core) zapcore.Core {
	if !h.Enabled() {
		return base
	}

	sentryCore, err := zapsentry.NewCore(zapsentry.Configuration{
		Level:             zapcore.ErrorLevel,
		EnableBreadcrumbs: true,
		BreadcrumbLevel:   zapcore.InfoLevel,
	}, zapsentry.NewSentryClientFromClient(sentrygo.CurrentHub().Client()))
	if err != nil {
		// zapsentry.Configuration is only invalid here if BreadcrumbLevel >
		// Level, which this package's own hard-coded values above can
		// never trigger — this branch exists for defensive completeness,
		// not because it's expected to run. `base` isn't usable to log
		// this through (that would recurse into the thing that just
		// failed to build), so stderr is the honest fallback.
		fmt.Fprintf(os.Stderr, "sentry: failed to build zap core, log-to-Sentry capture disabled: %v\n", err)
		return base
	}
	return zapcore.NewTee(base, sentryCore)
}

// RecoverMiddleware wraps an http.Handler so a panic inside it is reported
// to Sentry before being re-panicked (Repanic: true) — Go's own
// net/http.Server already recovers a handler panic per-connection (logs it,
// closes that connection, the process keeps running), so re-panicking here
// preserves that existing, already-safe behavior; this only adds the
// Sentry report on top of it. A disabled Handle returns next unchanged.
func (h *Handle) RecoverMiddleware(next http.Handler) http.Handler {
	if !h.Enabled() {
		return next
	}
	return sentryhttp.New(sentryhttp.Options{Repanic: true}).Handle(next)
}

// UnaryServerInterceptor recovers a panic inside a gRPC handler, reports it
// to Sentry (only if enabled — see below), and converts it to a
// codes.Internal status instead of letting it propagate.
//
// Unlike RecoverMiddleware, this recovery happens unconditionally, even on
// a disabled Handle: discovered while wiring this package in, grpc-go does
// NOT recover a panicking handler on its own (confirmed — nothing in this
// codebase's gRPC server chain does either; only gqlgen's generated
// GraphQL layer has its own separate recovery). A panic in any gRPC
// handler today crashes the whole process — in cmd/allinone specifically,
// that's all four backend services and the gateway at once. This
// interceptor is a genuine reliability fix that happens to live in the
// Sentry package because reporting the panic is its other job; see
// docs/DECISIONS.md for the full writeup.
func (h *Handle) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				if h.Enabled() {
					hub := sentrygo.CurrentHub().Clone()
					hub.Scope().SetTag("grpc.method", info.FullMethod)
					hub.RecoverWithContext(ctx, r)
					hub.Flush(2 * time.Second)
				}
				err = status.Error(codes.Internal, "internal error")
			}
		}()
		return handler(ctx, req)
	}
}
