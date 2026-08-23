// Package jobs implements jobs-service's business logic: job postings and
// the skills they require. See backend/cmd/jobs-service/README.md for how
// it fits the rest of the platform, and docs/DECISIONS.md for why the
// matching worker / job_matches / user_skill_snapshot (Phase 2, once Kafka
// exists) aren't part of this yet.
package jobs

import "time"

// Job is the GORM model backing jobs.jobs (see
// backend/migrations/jobs/00001_create_jobs.sql).
type Job struct {
	ID          string    `gorm:"column:id;primaryKey"`
	Title       string    `gorm:"column:title"`
	Description string    `gorm:"column:description"`
	CreatedAt   time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt   time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

// TableName pins this model to the jobs schema explicitly.
func (Job) TableName() string {
	return "jobs.jobs"
}

// RequiredSkill is the GORM model backing jobs.job_required_skills — the
// join between a job posting and the skills-service Skill IDs it
// requires. No FK to skills.skills is possible (different schema/role);
// skill_id is trusted as opaque and resolved for display by the gateway.
type RequiredSkill struct {
	JobID   string `gorm:"column:job_id;primaryKey"`
	SkillID string `gorm:"column:skill_id;primaryKey"`
}

// TableName pins this model to the jobs schema explicitly.
func (RequiredSkill) TableName() string {
	return "jobs.job_required_skills"
}
