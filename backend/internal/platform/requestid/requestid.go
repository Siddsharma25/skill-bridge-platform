// Package requestid generates and propagates a correlation ID (x-request-id)
// across process boundaries. See backend/internal/platform/README.md for
// why this exists: a single registration call crosses gateway -> gRPC ->
// (later) RabbitMQ -> NestJS -> Redis -> WebSocket, and none of that is
// debuggable after the fact without a value that ties every hop's log
// lines together.
package requestid

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// MetadataKey is the gRPC metadata (and, later, HTTP header / Kafka record
// header / AMQP property) key used everywhere to carry the request ID.
const MetadataKey = "x-request-id"

type contextKey struct{}

var ctxKey = contextKey{}

// Generate returns a random 16-byte hex-encoded ID. Not a UUID library on
// purpose — a request ID only needs to be unique enough to correlate logs
// within a short-lived trace, not globally unique for storage, so
// crypto/rand + hex avoids pulling in a dependency for that.
func Generate() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand.Read failing means the OS entropy source is broken;
		// in that case an all-zero ID is still preferable to a panic that
		// takes the whole request down for what is, at worst, a
		// debuggability degradation.
		return "00000000000000000000000000000000"
	}
	return hex.EncodeToString(b)
}

// NewContext stores id in ctx.
func NewContext(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey, id)
}

// FromContext retrieves the request ID previously stored by NewContext.
func FromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(ctxKey).(string)
	return id, ok && id != ""
}

// UnaryServerInterceptor extracts x-request-id from incoming gRPC metadata
// if present, generating a fresh one otherwise, stores it in the request
// context for handlers/logging, and echoes it back on the outgoing header
// so callers can log it too.
func UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		id := extractIncoming(ctx)
		if id == "" {
			id = Generate()
		}
		ctx = NewContext(ctx, id)
		_ = grpc.SetHeader(ctx, metadata.Pairs(MetadataKey, id))
		return handler(ctx, req)
	}
}

// UnaryClientInterceptor injects the request ID from ctx (generating one if
// this is the start of a new call chain) into outgoing gRPC metadata so the
// server side of UnaryServerInterceptor can pick it up.
func UnaryClientInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		id, ok := FromContext(ctx)
		if !ok {
			id = Generate()
		}
		ctx = metadata.AppendToOutgoingContext(ctx, MetadataKey, id)
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

func extractIncoming(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	vals := md.Get(MetadataKey)
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
}
