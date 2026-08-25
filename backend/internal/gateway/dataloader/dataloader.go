// Package dataloader batches every skill_id the gateway needs to resolve
// against skills-service within one GraphQL response into a single
// GetSkillsByIds call, fixing the Phase 1b N+1 left on Job.requiredSkills,
// Profile.skills (via UserSkill.skill), and UserSkill.skill — see the
// `TODO(phase 1c): dataloader` comments those resolvers carried and
// docs/DECISIONS.md.
//
// Library choice: github.com/graph-gophers/dataloader/v7, not gqlgen's
// community `dataloaden` code generator. dataloaden generates one
// hand-typed loader file per (key, value) pair via a `go generate`
// directive — a second codegen step alongside buf/gqlgen this project
// already has (see backend/CLAUDE.md's `make gen`). graph-gophers'
// generic Loader[K, V] (Go 1.18+ generics) gives the same type safety at
// runtime with zero extra codegen, which is the better fit for a project
// that already has two generators to keep synced. See docs/DECISIONS.md.
package dataloader

import (
	"context"
	"net/http"
	"time"

	dl "github.com/graph-gophers/dataloader/v7"

	skillsv1 "github.com/Siddsharma25/skill-bridge-platform/backend/gen/skills/v1"
	usersv1 "github.com/Siddsharma25/skill-bridge-platform/backend/gen/users/v1"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/gateway/graph/model"
)

// waitWindow is how long a Loader waits after its first Load() call before
// dispatching a batch — long enough to collect every Load issued while
// gqlgen resolves sibling fields of one response (e.g. every Job in a
// `jobs` query's requiredSkills resolver), short enough not to add
// perceptible latency to a single request. 2ms is generous relative to
// local gRPC call latency and goroutine scheduling on the same machine;
// see dataloader_test.go for a test that proves this actually batches N
// concurrent Loads into one skills-service call rather than assuming it
// from the config alone.
const waitWindow = 2 * time.Millisecond

// Loaders bundles every request-scoped dataloader this gateway uses.
// Exactly one Loaders is constructed per incoming HTTP request (see
// Middleware) — sharing one across requests would leak one request's
// resolved values, and its per-request in-memory cache, into a completely
// unrelated request. This is the classic dataloader bug the task
// specifically calls out to avoid.
type Loaders struct {
	SkillByID *loaderT
	UserByID  *userLoaderT
}

// loaderT is the concrete Loader type this package hands out for
// SkillByID — named (rather than spelling out dl.Loader[string,
// *model.Skill] at every call site) so dataloader_test.go's batching proof
// can hold a value of the exact same type production code constructs,
// via newBatchedLoaderForTest below, without duplicating the BatchFunc/
// wait-window wiring in the test.
type loaderT = dl.Loader[string, *model.Skill]

// userLoaderT is UserByID's concrete Loader type — same naming reasoning
// as loaderT above.
type userLoaderT = dl.Loader[string, *model.Profile]

type contextKey struct{}

var ctxKey = contextKey{}

// NewContext stores loaders in ctx.
func NewContext(ctx context.Context, loaders *Loaders) context.Context {
	return context.WithValue(ctx, ctxKey, loaders)
}

// FromContext retrieves the Loaders middleware stored for this request, or
// nil if none is present (e.g. a resolver invoked from a code path that
// doesn't go through Middleware — see graph/helpers.go's fallback).
func FromContext(ctx context.Context) *Loaders {
	l, _ := ctx.Value(ctxKey).(*Loaders)
	return l
}

// Middleware constructs a fresh Loaders for every incoming HTTP request and
// stores it in the request context, wrapping the GraphQL handler the same
// way authctx.Middleware and ratelimit.Middleware do (see
// cmd/api-gateway/main.go for the composition order).
func Middleware(skillsClient skillsv1.SkillsServiceClient, usersClient usersv1.UsersServiceClient) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			loaders := &Loaders{
				SkillByID: newSkillByIDLoader(skillsClient),
				UserByID:  newUserByIDLoader(usersClient),
			}
			next.ServeHTTP(w, r.WithContext(NewContext(r.Context(), loaders)))
		})
	}
}

// newSkillByIDLoader constructs the SkillByID loader Middleware attaches
// to every request's context: batchGetSkillsByID as its BatchFunc, waitWindow
// as its collection window.
func newSkillByIDLoader(skillsClient skillsv1.SkillsServiceClient) *loaderT {
	return dl.NewBatchedLoader(
		batchGetSkillsByID(skillsClient),
		dl.WithWait[string, *model.Skill](waitWindow),
	)
}

// newBatchedLoaderForTest exposes newSkillByIDLoader to
// dataloader_test.go's batching proof, so the test exercises the exact
// same constructor (BatchFunc + wait window) Middleware uses in
// production rather than a re-implementation that could silently drift
// from it.
func newBatchedLoaderForTest(skillsClient skillsv1.SkillsServiceClient) *loaderT {
	return newSkillByIDLoader(skillsClient)
}

// batchGetSkillsByID is the BatchFunc every SkillByID.Load(ctx, id) call
// ultimately feeds into: one GetSkillsByIds call carrying every distinct
// ID the Loader collected during its wait window. A dangling ID with no
// matching skill (e.g. skills-service data deleted after a job/user
// referenced it) resolves to a nil *model.Skill, not an error — same
// "silently omit" contract the Phase 1b resolveSkills helper had.
func batchGetSkillsByID(client skillsv1.SkillsServiceClient) dl.BatchFunc[string, *model.Skill] {
	return func(ctx context.Context, ids []string) []*dl.Result[*model.Skill] {
		results := make([]*dl.Result[*model.Skill], len(ids))

		resp, err := client.GetSkillsByIds(ctx, &skillsv1.GetSkillsByIdsRequest{Ids: ids})
		if err != nil {
			for i := range results {
				results[i] = &dl.Result[*model.Skill]{Error: err}
			}
			return results
		}

		bySkillID := make(map[string]*model.Skill, len(resp.GetSkills()))
		for _, sk := range resp.GetSkills() {
			bySkillID[sk.GetId()] = &model.Skill{ID: sk.GetId(), Name: sk.GetName(), Category: sk.GetCategory()}
		}

		for i, id := range ids {
			results[i] = &dl.Result[*model.Skill]{Data: bySkillID[id]}
		}
		return results
	}
}

// newUserByIDLoader constructs the UserByID loader Middleware attaches to
// every request's context: batchGetProfilesByID as its BatchFunc, the same
// waitWindow as SkillByID.
func newUserByIDLoader(usersClient usersv1.UsersServiceClient) *userLoaderT {
	return dl.NewBatchedLoader(
		batchGetProfilesByID(usersClient),
		dl.WithWait[string, *model.Profile](waitWindow),
	)
}

// batchGetProfilesByID is the BatchFunc every UserByID.Load(ctx, id) call
// feeds into: one GetProfilesByIds call carrying every distinct user_id
// the Loader collected during its wait window — e.g. every JobMatch.userId
// across a job's match list. A dangling id with no matching profile (or
// simply no profile row created yet) resolves to a nil *model.Profile, not
// an error, mirroring batchGetSkillsByID's "silently omit" contract.
func batchGetProfilesByID(client usersv1.UsersServiceClient) dl.BatchFunc[string, *model.Profile] {
	return func(ctx context.Context, ids []string) []*dl.Result[*model.Profile] {
		results := make([]*dl.Result[*model.Profile], len(ids))

		resp, err := client.GetProfilesByIds(ctx, &usersv1.GetProfilesByIdsRequest{UserIds: ids})
		if err != nil {
			for i := range results {
				results[i] = &dl.Result[*model.Profile]{Error: err}
			}
			return results
		}

		byUserID := make(map[string]*model.Profile, len(resp.GetProfiles()))
		for _, p := range resp.GetProfiles() {
			byUserID[p.GetUserId()] = &model.Profile{
				UserID:      p.GetUserId(),
				DisplayName: p.GetDisplayName(),
				Bio:         p.GetBio(),
			}
		}

		for i, id := range ids {
			results[i] = &dl.Result[*model.Profile]{Data: byUserID[id]}
		}
		return results
	}
}
