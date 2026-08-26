// Package graph holds api-gateway's GraphQL resolvers and the gqlgen
// dependency-injection root (Resolver). See gqlgen.yml and
// backend/internal/gateway/README.md.
package graph

// This file will not be regenerated automatically.
//
// It serves as dependency injection for your app, add any dependencies you require
// here.

import (
	authv1 "github.com/Siddsharma25/skill-bridge-platform/backend/gen/auth/v1"
	jobsv1 "github.com/Siddsharma25/skill-bridge-platform/backend/gen/jobs/v1"
	skillsv1 "github.com/Siddsharma25/skill-bridge-platform/backend/gen/skills/v1"
	usersv1 "github.com/Siddsharma25/skill-bridge-platform/backend/gen/users/v1"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/cache"
)

// Resolver holds every dependency the GraphQL layer needs to call out to
// backend gRPC services. It only ever holds generated gRPC clients (never
// a *gorm.DB or similar) — api-gateway is stateless and talks to every
// domain exclusively over gRPC, per docs/DECISIONS.md.
//
// Realtime is the one exception to "gRPC clients only," added in Phase
// 3.5: the onNotification subscription resolver (schema.resolvers.go)
// subscribes directly to Redis pub/sub rather than through a backend
// service, since api-gateway's own RabbitMQ-consuming realtime bridge
// (internal/gateway/realtime) is what publishes onto it — see
// docs/DECISIONS.md. Typed as the combined cache.RealtimeStore (not just
// cache.PubSub) so the same field also backs notificationHistory's
// Redis-Stream read (XREVRANGE) — one Redis dependency for everything
// this resolver layer needs beyond a backend gRPC call, not two separate
// fields for what's the same *cache.Client underneath. May be a disabled
// *cache.Client (REDIS_URL unset) — every method then reports unavailable
// rather than panicking, same degrade-gracefully convention as every
// other optional dependency here.
type Resolver struct {
	AuthClient   authv1.AuthServiceClient
	SkillsClient skillsv1.SkillsServiceClient
	UsersClient  usersv1.UsersServiceClient
	JobsClient   jobsv1.JobsServiceClient
	Realtime     cache.RealtimeStore
}
