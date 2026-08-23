// Package model holds both gqlgen-generated types (models_gen.go — DO NOT
// EDIT, regenerate via `make gen`) and the hand-written types in this file.
// Job, Profile, and UserSkill are bound here (see gqlgen.yml's `models:`
// section) instead of letting gqlgen generate plain structs for them,
// because each needs one extra Go field that isn't itself a GraphQL field:
// the raw skills-service ID(s) a parent object references, which the
// corresponding field resolver in schema.resolvers.go resolves against
// skills-service. See docs/DECISIONS.md for why that resolution is a
// deliberate small N+1 rather than solved with a dataloader in Phase 1b.
package model

// Job backs the GraphQL Job type. RequiredSkillIDs is not itself a
// GraphQL field — graph.Job.requiredSkills' resolver reads it to resolve
// each Skill from skills-service.
type Job struct {
	ID          string
	Title       string
	Description string

	// RequiredSkillIDs are the opaque skills-service IDs jobs-service
	// returned for this job. TODO(phase 1c): dataloader — the
	// requiredSkills resolver fires one skills-service call per Job
	// object today.
	RequiredSkillIDs []string
}

// Profile backs the GraphQL Profile type. It has no hidden field the way
// Job/UserSkill do — its "skills" field resolver (schema.resolvers.go)
// simply calls users-service's ListUserSkills(obj.UserID) directly, since
// UserID is already an exposed field. Profile is still bound to this
// custom struct (rather than left to gqlgen's default generation) purely
// so "skills" becomes a resolver at all: gqlgen only generates a plain
// data field for a schema field that trivially matches a Go struct field
// by name, and this struct deliberately has none named Skills.
type Profile struct {
	UserID      string
	DisplayName string
	Bio         string
}

// UserSkill backs the GraphQL UserSkill type. SkillID is not itself a
// GraphQL field — graph.UserSkill.skill's resolver reads it to resolve
// the full Skill from skills-service. Proficiency maps directly to the
// GraphQL `proficiency` field (same exported-field-name matching gqlgen
// uses for every other plain field in this codebase).
type UserSkill struct {
	SkillID     string
	Proficiency string
}
