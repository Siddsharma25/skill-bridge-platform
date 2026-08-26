// Package auth implements auth-service's business logic: password
// credential storage and JWT issuance. See backend/cmd/auth-service/README.md
// for why this is split into its own gRPC service rather than folded into
// users-service.
package auth

import "time"

// Credential is the GORM model backing auth.credentials (see
// backend/migrations/auth/00001_create_credentials.sql). It intentionally
// only ever leaves this package as a user_id + access_token pair — the
// password hash never crosses a package boundary into the gRPC layer.
type Credential struct {
	ID           string    `gorm:"column:id;primaryKey"`
	Email        string    `gorm:"column:email;uniqueIndex"`
	PasswordHash string    `gorm:"column:password_hash"`
	// Role is RBAC's whole footprint on this table: "user" (every
	// self-registered account) or "admin" (promoted out-of-band — see
	// docs/DECISIONS.md's RBAC notes for why there's no self-service path
	// to become one). Explicitly set to RoleUser on every insert
	// (server.go's Register) rather than left to this column's DB-level
	// DEFAULT — GORM sends every field's zero value in its INSERT
	// statement, which would silently override a DB default with an
	// empty string if this were left unset in Go.
	Role      string    `gorm:"column:role"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

// RoleUser and RoleAdmin are the only two roles this codebase knows about
// — a flat, fixed set rather than a separate roles table, since a
// two-value enum has no need for the extra indirection a normalized
// roles table would add at this project's scale (see docs/DECISIONS.md).
const (
	RoleUser  = "user"
	RoleAdmin = "admin"
)

// TableName pins this model to the auth schema explicitly — GORM has no
// concept of a default schema per model otherwise, and this project relies
// on schema-per-service isolation being correct.
func (Credential) TableName() string {
	return "auth.credentials"
}

// OAuthIdentity is the GORM model backing auth.oauth_identities (see
// backend/migrations/auth/00002_create_oauth_identities.sql) — a
// third-party identity (Google, today) linked to exactly one Credential.
// See oauth.go for the account-linking logic that decides whether a given
// Google login creates a new Credential or attaches to an existing one.
type OAuthIdentity struct {
	ID              string    `gorm:"column:id;primaryKey"`
	UserID          string    `gorm:"column:user_id"`
	Provider        string    `gorm:"column:provider"`
	ProviderSubject string    `gorm:"column:provider_subject"`
	Email           string    `gorm:"column:email"`
	CreatedAt       time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt       time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

// TableName pins this model to the auth schema explicitly.
func (OAuthIdentity) TableName() string {
	return "auth.oauth_identities"
}

// ProviderGoogle is the only OAuthIdentity.Provider value this codebase
// issues today; kept as a named constant rather than a magic string since
// it appears in both the write path (oauth.go) and any future query that
// filters by provider.
const ProviderGoogle = "google"
