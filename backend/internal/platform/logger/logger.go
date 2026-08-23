// Package logger provides the one structured logger every service in this
// monorepo uses. See backend/internal/platform/README.md for why zap and
// why a request-ID field is non-negotiable rather than opt-in.
package logger

import (
	"context"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/requestid"
)

// New builds a zap.Logger that writes structured JSON to stdout. JSON (not
// zap's human-friendly console encoder) is deliberate even for local dev:
// every log line will eventually be scraped by something (docker logs,
// kubectl logs, a future Loki/Grafana stack) that wants to parse it as
// records, not read it as prose. `service` is attached to every line so
// logs from every process can be told apart once they're aggregated.
func New(service string) (*zap.Logger, error) {
	cfg := zap.NewProductionConfig()
	cfg.EncoderConfig.TimeKey = "ts"
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	cfg.EncoderConfig.LevelKey = "level"
	cfg.EncoderConfig.MessageKey = "msg"

	l, err := cfg.Build()
	if err != nil {
		return nil, err
	}
	return l.With(zap.String("service", service)), nil
}

// FromContext returns a logger with the request's x-request-id field
// already attached, falling back to "unknown" so the field is *always*
// present rather than sometimes missing — a log line without a request ID
// is exactly the one you'll want to correlate during an incident and can't.
func FromContext(ctx context.Context, base *zap.Logger) *zap.Logger {
	id, ok := requestid.FromContext(ctx)
	if !ok || id == "" {
		id = "unknown"
	}
	return base.With(zap.String("request_id", id))
}
