// Package skills implements skills-service's business logic: the shared
// skill taxonomy every other service references by ID. See
// backend/cmd/skills-service/README.md for how it fits the rest of the
// platform.
package skills

import "time"

// Skill is the GORM model backing skills.skills (see
// backend/migrations/skills/00001_create_skills.sql).
type Skill struct {
	ID        string    `gorm:"column:id;primaryKey"`
	Name      string    `gorm:"column:name"`
	Category  string    `gorm:"column:category"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

// TableName pins this model to the skills schema explicitly — see
// backend/CLAUDE.md's "Adding a new service" section for why every model
// in this codebase does this rather than relying on a default schema.
func (Skill) TableName() string {
	return "skills.skills"
}
