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
)

// Resolver holds every dependency the GraphQL layer needs to call out to
// backend gRPC services. It only ever holds generated gRPC clients (never
// a *gorm.DB or similar) — api-gateway is stateless and talks to every
// domain exclusively over gRPC, per docs/DECISIONS.md.
type Resolver struct {
	AuthClient authv1.AuthServiceClient
}
