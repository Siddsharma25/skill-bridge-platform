package jobs

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	kafkaplat "github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/kafka"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/rabbitmq"
)

// matchThresholdOverlap is the matching worker's threshold: a candidate
// needs at least this many overlapping skills with a job's required list
// to count as a match. Picked deliberately simple — "at least one shared
// skill" — per docs/DECISIONS.md's Scope Cuts ("simple skill-overlap
// scoring, not ML"): it's the smallest rule that produces a
// non-empty, demonstrable match set without needing to justify a
// percentage cutoff against data this project doesn't have. Score
// (overlap / len(required_skill_ids), computed in HandleJobPosted) is
// still stored and published even though the threshold itself doesn't
// use it, so a future UI/ranking change has something to sort by without
// a schema change.
const matchThresholdOverlap = 1

// JobMatch is the GORM model backing jobs.job_matches — one row per
// (job_id, user_id) the matching worker scored above
// matchThresholdOverlap. See
// backend/migrations/jobs/00003_create_job_matches.sql.
type JobMatch struct {
	JobID     string    `gorm:"column:job_id;primaryKey"`
	UserID    string    `gorm:"column:user_id;primaryKey"`
	Score     float64   `gorm:"column:score"`
	MatchedAt time.Time `gorm:"column:matched_at"`
}

// TableName pins this model to the jobs schema explicitly.
func (JobMatch) TableName() string { return "jobs.job_matches" }

// MatchStore is the seam HandleJobPosted depends on to find candidates
// and persist match rows — an interface (same reasoning as SnapshotStore)
// so matcher_test.go can prove "processing the same job.posted event
// twice results in exactly one job_matches row per matched user, not
// two" with an in-memory fake, without a real database.
type MatchStore interface {
	// CandidatesForSkills returns, for every user_id in the snapshot with
	// at least one of requiredSkillIDs, how many of requiredSkillIDs they
	// have. A user with zero overlap is not present in the returned map
	// at all — the caller doesn't need to filter it out itself.
	CandidatesForSkills(ctx context.Context, requiredSkillIDs []string) (map[string]int, error)
	// UpsertMatch idempotently records (or updates) one job's match for
	// one user — ON CONFLICT (job_id, user_id) DO UPDATE, so reprocessing
	// the same job.posted event (Kafka delivery is at-least-once) never
	// creates a duplicate row, only refreshes score/matched_at in place.
	UpsertMatch(ctx context.Context, jobID, userID string, score float64) error
}

// gormMatchStore is the real MatchStore, backed by *gorm.DB.
type gormMatchStore struct{ db *gorm.DB }

// NewGormMatchStore constructs the real, database-backed MatchStore.
func NewGormMatchStore(db *gorm.DB) MatchStore { return &gormMatchStore{db: db} }

func (s *gormMatchStore) CandidatesForSkills(ctx context.Context, requiredSkillIDs []string) (map[string]int, error) {
	out := map[string]int{}
	if len(requiredSkillIDs) == 0 {
		return out, nil
	}

	type overlapRow struct {
		UserID  string
		Overlap int
	}
	var rows []overlapRow
	err := s.db.WithContext(ctx).
		Table("jobs.user_skill_snapshot").
		Select("user_id, count(*) as overlap").
		Where("skill_id IN ?", requiredSkillIDs).
		Group("user_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.UserID] = r.Overlap
	}
	return out, nil
}

func (s *gormMatchStore) UpsertMatch(ctx context.Context, jobID, userID string, score float64) error {
	m := JobMatch{JobID: jobID, UserID: userID, Score: score, MatchedAt: time.Now().UTC()}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "job_id"}, {Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"score", "matched_at"}),
	}).Create(&m).Error
}

// HandleJobPosted returns a kafka.Handler that decodes a job.posted event,
// scores every candidate in store's snapshot against the job's required
// skills (simple overlap count/percentage — see matchThresholdOverlap),
// idempotently upserts a jobs.job_matches row for everyone who clears the
// threshold, and publishes job.matched for each. A malformed payload or a
// job with no required skills is logged and skipped (returns nil), same
// poison-message reasoning as HandleUserSkillsUpdated.
//
// realtimePublisher is Phase 3.5's addition: for every matched user, a
// best-effort RealtimeNotification is also published to RabbitMQ's
// notifications.realtime queue (see internal/platform/rabbitmq and
// docs/DECISIONS.md), which api-gateway's realtime bridge republishes onto
// that user's Redis pub/sub channel for any live onNotification GraphQL
// subscription to receive. This is the phase's live-verification trigger
// (as opposed to auth-service's Register, which also publishes a realtime
// notification but can't be used to prove the subscription works live —
// see internal/auth/server.go and docs/DECISIONS.md for why): a user can
// register, log in, and open a genuinely listening subscription before a
// job matching their skills is created, so the publish here reliably has
// a chance of reaching a live subscriber. May be nil, in which case this
// realtime publish is simply skipped — same nil-safe pattern as the kafka
// publisher parameter.
func HandleJobPosted(store MatchStore, publisher kafkaplat.Publisher, realtimePublisher rabbitmq.Publisher, log *zap.Logger) kafkaplat.Handler {
	return func(ctx context.Context, msg kafkaplat.Message) error {
		var evt kafkaplat.JobPosted
		if err := json.Unmarshal(msg.Value, &evt); err != nil {
			log.Error("failed to decode job.posted event; skipping", zap.Error(err))
			return nil
		}
		if evt.JobID == "" {
			log.Warn("job.posted event missing job_id; skipping")
			return nil
		}
		if len(evt.RequiredSkillIDs) == 0 {
			log.Info("job.posted event has no required skills; nothing to match", zap.String("job_id", evt.JobID))
			return nil
		}

		candidates, err := store.CandidatesForSkills(ctx, evt.RequiredSkillIDs)
		if err != nil {
			log.Error("failed to score candidates for job.posted event", zap.Error(err), zap.String("job_id", evt.JobID))
			return err
		}

		matched := 0
		for userID, overlap := range candidates {
			if overlap < matchThresholdOverlap {
				continue
			}
			score := float64(overlap) / float64(len(evt.RequiredSkillIDs))

			if err := store.UpsertMatch(ctx, evt.JobID, userID, score); err != nil {
				log.Error("failed to upsert job match", zap.Error(err), zap.String("job_id", evt.JobID), zap.String("user_id", userID))
				continue
			}
			matched++

			if publisher != nil {
				payload, err := json.Marshal(kafkaplat.JobMatched{
					JobID: evt.JobID, UserID: userID, Score: score, MatchedAt: time.Now().UTC(),
				})
				if err != nil {
					log.Error("failed to marshal job.matched event; not published", zap.Error(err), zap.String("job_id", evt.JobID), zap.String("user_id", userID))
				} else {
					publisher.Publish(ctx, kafkaplat.TopicJobMatched, evt.JobID, payload)
				}
			}

			// Best-effort realtime "job_match" ping (Phase 3.5) — see this
			// function's doc comment for why this is the phase's live
			// verification path. A marshal/publish failure here is logged
			// and never fails this handler or blocks the next matched
			// user/redelivery — same "primary write already succeeded,
			// this notification is best-effort" reasoning as every other
			// realtime/email publish in this codebase.
			if realtimePublisher != nil {
				realtimePayload, err := json.Marshal(rabbitmq.RealtimeNotification{
					ID:        uuid.NewString(),
					UserID:    userID,
					Type:      rabbitmq.RealtimeNotificationTypeJobMatch,
					Message:   "A new job matches your skills!",
					CreatedAt: time.Now().UTC(),
					JobID:     evt.JobID,
					Score:     score,
				})
				if err != nil {
					log.Error("failed to marshal realtime job_match notification; not published", zap.Error(err), zap.String("job_id", evt.JobID), zap.String("user_id", userID))
				} else {
					realtimePublisher.Publish(ctx, rabbitmq.QueueNotificationsRealtime, realtimePayload)
				}
			}
		}

		log.Info("processed job.posted event", zap.String("job_id", evt.JobID),
			zap.Int("candidates_scored", len(candidates)), zap.Int("matched_users", matched))
		return nil
	}
}
