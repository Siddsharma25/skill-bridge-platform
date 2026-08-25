package jobs

import (
	"context"
	"encoding/json"

	"go.uber.org/zap"

	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/cache"
	kafkaplat "github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/kafka"
)

// HandleSkillUpdated returns a kafka.Handler that evicts jobs-service's
// own jobs:all cache entry whenever skills-service publishes
// skill.updated. This is the one place in this codebase where
// event-driven cache invalidation is actually the right tool — every
// other cache DEL in this project is write-through, same-service (see
// docs/DECISIONS.md's Caching section) — because a skill's name/category
// changing is only ever visible cross-service, and jobs-service has no
// other way to learn about it without either polling skills-service or
// (worse) reading its schema directly.
//
// jobs-service's only other cache entries (jobs:<id>, per job — see
// server.go's jobCacheKey) aren't evicted here: skill.updated only
// carries a skill_id, not the set of job IDs that reference it, and with
// a 30s TTL already on those entries (jobsCacheTTL), the bounded
// staleness window was judged not worth adding a Redis SCAN just to
// enumerate them — see docs/DECISIONS.md's Phase 2 notes.
func HandleSkillUpdated(c cache.Cache, log *zap.Logger) kafkaplat.Handler {
	return func(ctx context.Context, msg kafkaplat.Message) error {
		var evt kafkaplat.SkillUpdated
		if err := json.Unmarshal(msg.Value, &evt); err != nil {
			log.Error("failed to decode skill.updated event; skipping", zap.Error(err))
			return nil
		}

		if c != nil {
			c.Del(ctx, jobsAllCacheKey)
		}
		log.Info("evicted jobs:all cache after skill.updated", zap.String("skill_id", evt.SkillID))
		return nil
	}
}
