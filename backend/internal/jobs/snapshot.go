package jobs

import (
	"context"
	"encoding/json"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	kafkaplat "github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/kafka"
)

// UserSkillSnapshot is the GORM model backing jobs.user_skill_snapshot —
// jobs-service's own event-carried-state-transfer projection of a user's
// current skill list, fed entirely by consuming users-service's
// user.skills.updated Kafka event (see
// backend/migrations/jobs/00002_create_user_skill_snapshot.sql and
// docs/DECISIONS.md's "why jobs-service doesn't just query
// users-service's database"). This is what lets the matching worker
// (matcher.go) score candidates without jobs-service ever calling
// users-service directly, and stay fully functional even if
// users-service is down.
type UserSkillSnapshot struct {
	UserID      string    `gorm:"column:user_id;primaryKey"`
	SkillID     string    `gorm:"column:skill_id;primaryKey"`
	Proficiency string    `gorm:"column:proficiency"`
	UpdatedAt   time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

// TableName pins this model to the jobs schema explicitly.
func (UserSkillSnapshot) TableName() string { return "jobs.user_skill_snapshot" }

// SnapshotStore is the seam HandleUserSkillsUpdated depends on to persist
// a replaced snapshot — an interface (same reasoning as cache.Cache and
// kafka.Publisher elsewhere in this codebase) so snapshot_test.go can
// prove idempotent-reprocessing behavior (processing the same event twice
// leaves the snapshot correct, not duplicated) with an in-memory fake,
// without a real database.
type SnapshotStore interface {
	// ReplaceUserSkills atomically replaces every existing snapshot row
	// for userID with skills — delete-then-insert, not an upsert plus a
	// separate stale-row cleanup, so a full replace is one logical
	// operation. This is what makes repeated/out-of-order
	// user.skills.updated delivery safe: reprocessing the same (or a
	// newer) event always leaves the snapshot as exactly what the event
	// said, never a duplicate row per skill and never a leftover row for
	// a skill the user no longer has.
	ReplaceUserSkills(ctx context.Context, userID string, skills []UserSkillSnapshot) error
}

// gormSnapshotStore is the real SnapshotStore, backed by *gorm.DB.
type gormSnapshotStore struct{ db *gorm.DB }

// NewGormSnapshotStore constructs the real, database-backed SnapshotStore.
func NewGormSnapshotStore(db *gorm.DB) SnapshotStore { return &gormSnapshotStore{db: db} }

func (s *gormSnapshotStore) ReplaceUserSkills(ctx context.Context, userID string, skills []UserSkillSnapshot) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ?", userID).Delete(&UserSkillSnapshot{}).Error; err != nil {
			return err
		}
		if len(skills) == 0 {
			return nil
		}
		return tx.Create(&skills).Error
	})
}

// HandleUserSkillsUpdated returns a kafka.Handler that decodes a
// user.skills.updated event and replaces store's snapshot for that user.
// A malformed payload is logged and skipped (returns nil, not an error)
// rather than looping forever on a poison message — see
// internal/platform/kafka.Consumer.Run's doc comment for why handler
// errors here aren't retried with backoff.
func HandleUserSkillsUpdated(store SnapshotStore, log *zap.Logger) kafkaplat.Handler {
	return func(ctx context.Context, msg kafkaplat.Message) error {
		var evt kafkaplat.UserSkillsUpdated
		if err := json.Unmarshal(msg.Value, &evt); err != nil {
			log.Error("failed to decode user.skills.updated event; skipping", zap.Error(err))
			return nil
		}
		if evt.UserID == "" {
			log.Warn("user.skills.updated event missing user_id; skipping")
			return nil
		}

		rows := make([]UserSkillSnapshot, len(evt.Skills))
		for i, sk := range evt.Skills {
			rows[i] = UserSkillSnapshot{UserID: evt.UserID, SkillID: sk.SkillID, Proficiency: sk.Proficiency}
		}

		if err := store.ReplaceUserSkills(ctx, evt.UserID, rows); err != nil {
			log.Error("failed to replace user skill snapshot", zap.Error(err), zap.String("user_id", evt.UserID))
			return err
		}
		log.Info("updated user skill snapshot", zap.String("user_id", evt.UserID), zap.Int("skill_count", len(rows)))
		return nil
	}
}
