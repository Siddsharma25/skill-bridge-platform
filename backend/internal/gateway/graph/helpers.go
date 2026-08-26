package graph

// This file is hand-written and never touched by `make gen`, same
// reasoning as errors.go: `gqlgen generate` only preserves method bodies
// on the resolver-embedding types it generated (jobResolver,
// mutationResolver, etc.) — any other top-level declaration living in
// schema.resolvers.go, including a plain helper function or a method on
// *Resolver itself, gets swept into a commented-out "you have one more
// chance to move this" block on every regeneration. Keeping these here
// instead means editing the schema and re-running `make gen` never
// silently breaks the build.

import (
	"context"

	jobsv1 "github.com/Siddsharma25/skill-bridge-platform/backend/gen/jobs/v1"
	skillsv1 "github.com/Siddsharma25/skill-bridge-platform/backend/gen/skills/v1"
	usersv1 "github.com/Siddsharma25/skill-bridge-platform/backend/gen/users/v1"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/gateway/authctx"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/gateway/dataloader"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/gateway/graph/model"
)

// resolveSkills resolves each of ids against skills-service's full
// ListSkills response, filtering locally — skills-service has no
// single-ID lookup RPC (see skills.proto), so this is the only way to
// turn an opaque skill_id back into a name/category today. This is the
// Phase 1b fallback path: resolveSkillsViaLoader (below) uses this only
// when no per-request dataloader is present in ctx (e.g. a resolver
// invoked from a unit test that doesn't go through
// dataloader.Middleware) — every live request goes through the batched
// path instead. See docs/DECISIONS.md.
func (r *Resolver) resolveSkills(ctx context.Context, ids []string) ([]*model.Skill, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	resp, err := r.SkillsClient.ListSkills(ctx, &skillsv1.ListSkillsRequest{})
	if err != nil {
		return nil, translateGRPCError(err)
	}
	bySkillID := make(map[string]*skillsv1.Skill, len(resp.GetSkills()))
	for _, sk := range resp.GetSkills() {
		bySkillID[sk.GetId()] = sk
	}
	out := make([]*model.Skill, 0, len(ids))
	for _, id := range ids {
		if sk, ok := bySkillID[id]; ok {
			out = append(out, toModelSkill(sk))
		}
	}
	return out, nil
}

// resolveSkillsViaLoader is the Phase 1c replacement for calling
// resolveSkills directly from Job.requiredSkills/UserSkill.skill: it
// issues one dataloader.Loaders.SkillByID.Load per id up front (not
// interleaved with awaiting each thunk), so every id this single parent
// object needs is queued before any of them blocks — combined with
// gqlgen's default concurrent resolution of sibling list elements
// (graphql.MarshalSliceConcurrently, see generated.go), every Job in one
// `jobs` response — and every UserSkill in one profile's skills list —
// ends up queuing its ids within the loader's wait window, so
// skills-service sees exactly one batched GetSkillsByIds call for the
// whole response instead of one ListSkills call per object (see
// dataloader_test.go's batching proof and docs/DECISIONS.md).
//
// Falls back to resolveSkills (the Phase 1b whole-taxonomy scan) when ctx
// carries no Loaders — a resolver invoked outside dataloader.Middleware
// (e.g. directly from a resolver-level test) still works, just without
// batching.
func (r *Resolver) resolveSkillsViaLoader(ctx context.Context, ids []string) ([]*model.Skill, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	loaders := dataloader.FromContext(ctx)
	if loaders == nil {
		return r.resolveSkills(ctx, ids)
	}

	thunks := make([]func() (*model.Skill, error), len(ids))
	for i, id := range ids {
		thunks[i] = loaders.SkillByID.Load(ctx, id)
	}

	out := make([]*model.Skill, 0, len(ids))
	for _, thunk := range thunks {
		sk, err := thunk()
		if err != nil {
			return nil, translateGRPCError(err)
		}
		if sk != nil {
			out = append(out, sk)
		}
	}
	return out, nil
}

// resolveDisplayNamesViaLoader resolves each of ids (JobMatch.userId
// values) to a display name via the per-request UserByID dataloader,
// batching every distinct id across one job's match list into a single
// GetProfilesByIds call — same reasoning as resolveSkillsViaLoader above.
// A dangling id, or a real profile with no display name set yet, both
// fall back to the id itself (see schema.graphqls' JobMatch.displayName
// doc comment) rather than surfacing an empty string or an error — a job
// match should always show *something* human-scannable.
func (r *Resolver) resolveDisplayNamesViaLoader(ctx context.Context, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	loaders := dataloader.FromContext(ctx)
	if loaders == nil {
		return r.resolveDisplayNames(ctx, ids)
	}

	thunks := make([]func() (*model.Profile, error), len(ids))
	for i, id := range ids {
		thunks[i] = loaders.UserByID.Load(ctx, id)
	}

	out := make([]string, len(ids))
	for i, thunk := range thunks {
		p, err := thunk()
		if err != nil {
			return nil, translateGRPCError(err)
		}
		if p != nil && p.DisplayName != "" {
			out[i] = p.DisplayName
		} else {
			out[i] = ids[i]
		}
	}
	return out, nil
}

// resolveDisplayNames is resolveDisplayNamesViaLoader's no-dataloader
// fallback (a resolver invoked outside dataloader.Middleware, e.g. a
// resolver-level test) — same reasoning as resolveSkills above.
func (r *Resolver) resolveDisplayNames(ctx context.Context, ids []string) ([]string, error) {
	resp, err := r.UsersClient.GetProfilesByIds(ctx, &usersv1.GetProfilesByIdsRequest{UserIds: ids})
	if err != nil {
		return nil, translateGRPCError(err)
	}
	byUserID := make(map[string]string, len(resp.GetProfiles()))
	for _, p := range resp.GetProfiles() {
		byUserID[p.GetUserId()] = p.GetDisplayName()
	}
	out := make([]string, len(ids))
	for i, id := range ids {
		if name, ok := byUserID[id]; ok && name != "" {
			out[i] = name
		} else {
			out[i] = id
		}
	}
	return out, nil
}

// requireUserID returns the verified caller's user ID from ctx, or a
// GraphQL error if the request had no valid bearer token. Every resolver
// that requires an authenticated caller (myProfile, updateProfile,
// addUserSkill — the first authenticated operations in this project)
// calls this first. See internal/gateway/authctx and docs/DECISIONS.md.
// notificationHistoryLimit is how many recent notifications the
// notificationHistory resolver (schema.resolvers.go) asks the Redis
// Stream for — deliberately the same number cache.streamMaxLen trims
// each per-user stream down to, since asking for more than the stream
// ever retains would just always come back short. Lives here, not in
// schema.resolvers.go, because gqlgen's own header comment on that file
// says exactly why: a bare top-level declaration with no matching schema
// field gets swept into a commented-out "move this out" block on every
// regeneration.
const notificationHistoryLimit = 50

func requireUserID(ctx context.Context) (string, error) {
	userID, ok := authctx.UserID(ctx)
	if !ok {
		return "", &gqlError{msg: "authentication required"}
	}
	return userID, nil
}

// roleAdmin is intentionally the plain string literal "admin" — not
// auth.RoleAdmin — so this gateway-layer package never imports
// auth-service's internal package just to name a role: RBAC's rule here
// is "the verified token's role claim, whatever auth-service put there,
// must equal this string," and that string is part of this codebase's
// wire contract (a JWT claim value), not an implementation detail of
// auth-service worth coupling two otherwise-independent packages over.
const roleAdmin = "admin"

// requireAdmin is requireUserID's RBAC-aware sibling: the caller must be
// authenticated *and* their verified token's role claim must be
// roleAdmin. Used by createSkill (see schema.resolvers.go) — see
// docs/DECISIONS.md's RBAC notes for why skill-taxonomy management is the
// one operation gated so far, and why "authenticated but not admin" and
// "not authenticated at all" return different, honest error messages
// rather than the same generic "forbidden" (this is authorization
// feedback for a legitimate account holder, not a security boundary where
// distinguishing the two would leak anything an attacker doesn't already
// know from simply trying both).
func requireAdmin(ctx context.Context) (string, error) {
	userID, err := requireUserID(ctx)
	if err != nil {
		return "", err
	}
	if role, ok := authctx.Role(ctx); !ok || role != roleAdmin {
		return "", &gqlError{msg: "admin role required"}
	}
	return userID, nil
}

func toModelSkill(s *skillsv1.Skill) *model.Skill {
	if s == nil {
		return nil
	}
	return &model.Skill{ID: s.GetId(), Name: s.GetName(), Category: s.GetCategory()}
}

func toModelJob(j *jobsv1.Job) *model.Job {
	if j == nil {
		return nil
	}
	return &model.Job{
		ID:               j.GetId(),
		Title:            j.GetTitle(),
		Description:      j.GetDescription(),
		RequiredSkillIDs: j.GetRequiredSkillIds(),
	}
}

func toModelProfile(p *usersv1.Profile) *model.Profile {
	if p == nil {
		return nil
	}
	return &model.Profile{UserID: p.GetUserId(), DisplayName: p.GetDisplayName(), Bio: p.GetBio()}
}
