// Package authctx carries api-gateway's verified caller identity (a JWT
// `sub` claim, once Middleware has verified the bearer token against
// auth-service's JWKS) through a request's context.Context, and forwards
// it to backend services as gRPC metadata.
//
// This is Phase 1b's first authenticated flow — Phase 1a deliberately had
// nothing to protect yet (see docs/DECISIONS.md), so request-time JWT
// verification middleware didn't exist until myProfile/updateProfile/
// addUserSkill gave it something to guard.
package authctx

import (
	"context"
	"errors"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/jwks"
)

// MetadataKey is the gRPC metadata key the verified user ID is forwarded
// under to any backend service that wants it — mirrors
// internal/platform/requestid's pattern of using a plain metadata key for
// a cross-cutting concern rather than a bespoke field on every proto
// message. Only users-service's client connection has the forwarding
// interceptor wired on today (see cmd/api-gateway/main.go); no other
// backend service reads it yet.
const MetadataKey = "user_id"

type contextKey struct{}

var ctxKey = contextKey{}

// NewContext stores the verified user ID (a JWT `sub` claim) in ctx.
func NewContext(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, ctxKey, userID)
}

// UserID retrieves the verified user ID previously stored by NewContext.
// ok is false if the incoming request had no valid bearer token — callers
// that require an authenticated caller (see schema.resolvers.go's
// requireUserID) treat that as a GraphQL error, not a panic or a silent
// empty-string identity.
func UserID(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(ctxKey).(string)
	return id, ok && id != ""
}

// errNoSubject is returned by VerifyToken when a token parses and
// verifies fine but carries no `sub` claim — a token this codebase's own
// jwks.Sign would never produce, but a defensive check regardless (same
// reasoning Middleware already applied inline before this was extracted).
var errNoSubject = errors.New("authctx: token has no subject claim")

// VerifyToken verifies a raw bearer token (no "Bearer " prefix) against
// jwksClient and returns its `sub` claim, or an error if the token is
// missing, malformed, expired, or fails signature verification. This is
// the one place token verification actually happens — both Middleware
// (HTTP requests, below) and the WebSocket `connection_init` handshake
// (see cmd/api-gateway/main.go's wsInitFunc, Phase 3.5) call this rather
// than each re-implementing "parse + verify + extract subject," so there
// is exactly one code path to get that logic right in.
func VerifyToken(ctx context.Context, jwksClient *jwks.Client, token string) (string, error) {
	claims, err := jwks.Verify(ctx, jwksClient, token)
	if err != nil {
		return "", err
	}
	if claims.Subject == "" {
		return "", errNoSubject
	}
	return claims.Subject, nil
}

// UnaryClientInterceptor forwards the verified user ID (if any) from ctx
// as outgoing gRPC metadata, so a backend service could trust the
// gateway's verification instead of re-checking a token itself (see
// docs/DECISIONS.md's "gateway is the only JWT verifier" decision). The
// user ID is also passed explicitly as a request field on every
// users-service RPC that needs it (GetProfile/UpdateProfile/
// AddUserSkill/ListUserSkills all take user_id directly) — the metadata
// forwarding here is the architectural pattern the plan calls for, kept
// consistent even though today's users-service handlers read the
// explicit field rather than the metadata.
func UnaryClientInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if userID, ok := UserID(ctx); ok {
			ctx = metadata.AppendToOutgoingContext(ctx, MetadataKey, userID)
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}
