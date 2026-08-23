// Package users implements users-service's business logic: profile data
// and a user's claimed skills. See backend/cmd/users-service/README.md for
// how it fits the rest of the platform, and docs/DECISIONS.md for the
// lazy-profile-creation note (there's no Kafka `user.registered` consumer
// yet, so GetProfile/UpdateProfile provision the row on first touch
// instead).
package users

import "time"

// Profile is the GORM model backing users.profiles (see
// backend/migrations/users/00001_create_profiles_and_user_skills.sql).
type Profile struct {
	UserID      string    `gorm:"column:user_id;primaryKey"`
	DisplayName string    `gorm:"column:display_name"`
	Bio         string    `gorm:"column:bio"`
	CreatedAt   time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt   time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

// TableName pins this model to the users schema explicitly.
func (Profile) TableName() string {
	return "users.profiles"
}

// UserSkill is the GORM model backing users.user_skills — a user's claimed
// skill (by skills-service ID) and self-reported proficiency. Composite
// primary key (user_id, skill_id) enforces one row per pair, which
// AddUserSkill's upsert relies on.
type UserSkill struct {
	UserID      string    `gorm:"column:user_id;primaryKey"`
	SkillID     string    `gorm:"column:skill_id;primaryKey"`
	Proficiency string    `gorm:"column:proficiency"`
	CreatedAt   time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt   time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

// TableName pins this model to the users schema explicitly.
func (UserSkill) TableName() string {
	return "users.user_skills"
}
