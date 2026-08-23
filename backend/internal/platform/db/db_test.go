package db

import (
	"testing"
	"time"
)

// These tests intentionally never open a real network connection — there
// is no live Supabase project yet (see docs/DECISIONS.md), so what's
// verifiable right now is the config-wiring logic: defaults, DSN plumbing,
// and Connect's early-return behavior when unconfigured.

func TestLoadConfig_Defaults(t *testing.T) {
	env := map[string]string{}
	cfg := LoadConfig(func(k string) string { return env[k] })

	if cfg.DSN != "" {
		t.Errorf("expected empty DSN when DATABASE_URL unset, got %q", cfg.DSN)
	}
	if cfg.MaxOpenConns != 5 {
		t.Errorf("expected default MaxOpenConns=5 (Supavisor pooler budget), got %d", cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns != 2 {
		t.Errorf("expected default MaxIdleConns=2, got %d", cfg.MaxIdleConns)
	}
	if cfg.ConnMaxLifetime != 30*time.Minute {
		t.Errorf("expected default ConnMaxLifetime=30m, got %s", cfg.ConnMaxLifetime)
	}
}

func TestLoadConfig_ReadsDSNFromEnv(t *testing.T) {
	//nolint:gosec // placeholder example DSN in a test, not a real credential
	const want = "postgres://user.projectref:pw@aws-0-region.pooler.supabase.com:6543/postgres"
	env := map[string]string{"DATABASE_URL": want}
	cfg := LoadConfig(func(k string) string { return env[k] })

	if cfg.DSN != want {
		t.Errorf("expected DSN %q, got %q", want, cfg.DSN)
	}
}

func TestConnect_NoDSNReturnsErrNoDSN(t *testing.T) {
	_, err := Connect(Config{})
	if err != ErrNoDSN {
		t.Errorf("expected ErrNoDSN when DSN is empty, got %v", err)
	}
}
