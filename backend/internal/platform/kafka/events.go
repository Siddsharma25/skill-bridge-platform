// Package kafka is Phase 2's thin wrapper around a real Kafka client
// (github.com/twmb/franz-go — see docs/DECISIONS.md's "Phase 2
// implementation notes" for why this library over the alternatives),
// giving every service the same two things internal/platform/cache gives
// them for Redis: a degrade-gracefully-when-unconfigured Producer, and a
// Consumer that integrates with internal/platform/shutdown so SIGINT
// stops it cleanly instead of leaving a zombie consumer-group member (see
// that package's doc comment for why that specifically matters for
// Kafka).
//
// This file holds the event payload shapes and topic names every
// publisher/consumer pair in this codebase shares. They're plain
// JSON-tagged structs, not protobuf messages — see docs/DECISIONS.md for
// why a JSON envelope was chosen over protobuf for Kafka payloads despite
// this project being proto-first for gRPC.
package kafka

import "time"

// Topic names. Every topic in this codebase is created implicitly on
// first publish (the local broker's KAFKA_AUTO_CREATE_TOPICS_ENABLE, see
// docker/docker-compose.infra.yml) rather than pre-declared — fine for a
// single-broker learning setup, not something a real production cluster
// would rely on.
const (
	// TopicUserSkillsUpdated carries a user's full current skill list
	// (event-carried state transfer, not a delta — see
	// docs/DECISIONS.md's "why jobs-service doesn't just query
	// users-service's database") every time users-service's AddUserSkill
	// changes it. Keyed by user_id.
	TopicUserSkillsUpdated = "user.skills.updated"

	// TopicJobPosted carries a newly created job's required skill IDs,
	// published by jobs-service's CreateJob and consumed by jobs-service's
	// own matching worker (a separate consumer group in the same process —
	// see internal/jobs/matcher.go). Keyed by job_id.
	TopicJobPosted = "job.posted"

	// TopicJobMatched carries one (job_id, user_id, score) match, published
	// by the matching worker after it idempotently upserts the
	// corresponding jobs.job_matches row. Keyed by job_id. Nothing
	// currently consumes this (a future WebSocket/notification phase
	// would) — publishing it now is still worthwhile since it's the
	// observable proof the matching worker did its job, and matches the
	// architecture plan's Phase 2 event list.
	TopicJobMatched = "job.matched"

	// TopicSkillUpdated is published by skills-service's CreateSkill (in
	// addition to its own write-through skills:all cache DEL) and consumed
	// by jobs-service to evict its own jobs:all cache entry — the one
	// place in this codebase where event-driven cache invalidation is
	// actually the right tool, because it's genuinely cross-service (see
	// docs/DECISIONS.md's Caching section). Keyed by skill_id.
	TopicSkillUpdated = "skill.updated"
)

// UserSkillsUpdated is TopicUserSkillsUpdated's payload: the user's
// *entire* current skill list, not a delta. jobs-service's snapshot
// consumer (internal/jobs/snapshot.go) treats this as authoritative and
// replaces its whole projection for this user_id, which is what makes
// repeated/out-of-order delivery safe (see docs/DECISIONS.md).
type UserSkillsUpdated struct {
	UserID    string                   `json:"user_id"`
	Skills    []UserSkillsUpdatedSkill `json:"skills"`
	UpdatedAt time.Time                `json:"updated_at"`
}

// UserSkillsUpdatedSkill is one entry in UserSkillsUpdated.Skills.
type UserSkillsUpdatedSkill struct {
	SkillID     string `json:"skill_id"`
	Proficiency string `json:"proficiency"`
}

// JobPosted is TopicJobPosted's payload.
type JobPosted struct {
	JobID            string    `json:"job_id"`
	RequiredSkillIDs []string  `json:"required_skill_ids"`
	PostedAt         time.Time `json:"posted_at"`
}

// JobMatched is TopicJobMatched's payload.
type JobMatched struct {
	JobID     string    `json:"job_id"`
	UserID    string    `json:"user_id"`
	Score     float64   `json:"score"`
	MatchedAt time.Time `json:"matched_at"`
}

// SkillUpdated is TopicSkillUpdated's payload. Only skill_id is carried —
// consumers that need the skill's new name/category re-fetch it from
// skills-service rather than trusting a possibly-stale copy in the event
// itself; today's only consumer (jobs-service's cache invalidator) does
// not even need that much, since it only evicts a cache entry.
type SkillUpdated struct {
	SkillID string `json:"skill_id"`
}
