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
	CreatedAt    time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt    time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

// TableName pins this model to the auth schema explicitly — GORM has no
// concept of a default schema per model otherwise, and this project relies
// on schema-per-service isolation being correct.
func (Credential) TableName() string {
	return "auth.credentials"
}
