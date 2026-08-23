// Package db is the one place every service opens its Postgres connection,
// because the connection settings here aren't arbitrary — they're forced
// by using Supabase's Supavisor pooler in transaction mode (port 6543),
// which every service needs to share since five+ services would otherwise
// exhaust a free-tier direct-connection limit. Transaction-mode pooling
// means a connection can be handed to a different client between
// statements, which breaks server-side prepared statements — hence
// PreferSimpleProtocol below. See backend/internal/platform/README.md and
// docs/DECISIONS.md for the full reasoning.
package db

import (
	"errors"
	"os"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ErrNoDSN is returned by Connect when no DSN is configured. Callers in
// this codebase (see cmd/auth-service) treat this as non-fatal in
// development — there is no live Supabase project yet, so the service
// should still start and serve health checks, just report /readyz as
// not-ready and log a clear warning.
var ErrNoDSN = errors.New("db: DATABASE_URL is not set")

// Config holds connection-pool settings. Defaults are sized for Supavisor's
// transaction-mode pooler, not for a direct Postgres connection — do not
// casually bump MaxOpenConns without re-reading the Supavisor connection
// limit math in docs/DECISIONS.md, since every service instance multiplies
// this against the shared pool budget.
type Config struct {
	// DSN is the full Postgres connection string, e.g.
	// postgres://user.<project-ref>:password@aws-0-region.pooler.supabase.com:6543/postgres
	DSN string

	// MaxOpenConns is deliberately small (~5) because Supabase free-tier
	// Supavisor pooler connection budgets are shared across every service
	// that's running at once.
	MaxOpenConns int
	// MaxIdleConns is kept <= MaxOpenConns; idle pooled connections still
	// count against Supavisor's budget even when unused.
	MaxIdleConns int
	// ConnMaxLifetime forces periodic reconnection so a long-lived idle
	// connection can't be silently dropped by the pooler and only
	// discovered on the next query.
	ConnMaxLifetime time.Duration
}

// defaultConfig returns the recommended defaults for a Supavisor
// transaction-mode connection.
func defaultConfig() Config {
	return Config{
		MaxOpenConns:    5,
		MaxIdleConns:    2,
		ConnMaxLifetime: 30 * time.Minute,
	}
}

// LoadConfig builds a Config from environment variables via getenv (an
// injectable lookup function so this is unit-testable without a real
// process environment — see db_test.go). Only DATABASE_URL is read today;
// pool-size overrides can be added here later if a service ever needs to
// deviate from the shared default.
func LoadConfig(getenv func(string) string) Config {
	cfg := defaultConfig()
	cfg.DSN = getenv("DATABASE_URL")
	return cfg
}

// LoadConfigFromEnv is the non-test-injected convenience wrapper most
// callers want.
func LoadConfigFromEnv() Config {
	return LoadConfig(os.Getenv)
}

// Connect opens a GORM connection using cfg. It returns ErrNoDSN if no DSN
// is configured — callers decide whether that's fatal (it is not, for
// auth-service in Phase 1a, since there's no live Supabase project yet).
func Connect(cfg Config) (*gorm.DB, error) {
	if cfg.DSN == "" {
		return nil, ErrNoDSN
	}

	gormDB, err := gorm.Open(postgres.New(postgres.Config{
		DSN: cfg.DSN,
		// Required for Supavisor transaction-mode pooling: the pooler may
		// hand a physical connection to a different logical session
		// between statements, so server-side prepared statements (which
		// GORM/pgx use by default) can silently break. Simple protocol
		// sends parameters inline instead of via PREPARE/EXECUTE.
		PreferSimpleProtocol: true,
	}), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, err
	}

	sqlDB, err := gormDB.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)

	return gormDB, nil
}

// Ping is a small helper for /readyz checks: it verifies the pool can still
// reach Postgres without needing every caller to reach into *sql.DB
// themselves.
func Ping(gormDB *gorm.DB) error {
	sqlDB, err := gormDB.DB()
	if err != nil {
		return err
	}
	return sqlDB.Ping()
}
