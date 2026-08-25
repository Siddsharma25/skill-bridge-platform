package graph

// This test is the resolver/live-endpoint-level companion to
// internal/gateway/dataloader/dataloader_test.go's loader-level batching
// proof. That test proves graph-gophers/dataloader itself collapses
// concurrent Load() calls into one BatchFunc invocation; this one proves
// the wiring actually in front of it — dataloader.Middleware wrapping the
// real gqlgen HTTP handler, exactly as cmd/api-gateway/main.go composes it
// — produces the same result for a real `jobs { requiredSkills { ... } }`
// query returning multiple jobs with overlapping required-skill IDs. See
// docs/DECISIONS.md.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/99designs/gqlgen/graphql/handler"
	"google.golang.org/grpc"

	jobsv1 "github.com/Siddsharma25/skill-bridge-platform/backend/gen/jobs/v1"
	skillsv1 "github.com/Siddsharma25/skill-bridge-platform/backend/gen/skills/v1"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/gateway/dataloader"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/gateway/graph/generated"
)

// fakeJobsClient serves a fixed set of jobs, deliberately sharing skill IDs
// across jobs the same way real postings for related roles would (e.g.
// several backend roles all requiring "Go").
type fakeJobsClient struct {
	jobsv1.JobsServiceClient
	jobs []*jobsv1.Job
}

func (f *fakeJobsClient) ListJobs(_ context.Context, _ *jobsv1.ListJobsRequest, _ ...grpc.CallOption) (*jobsv1.ListJobsResponse, error) {
	return &jobsv1.ListJobsResponse{Jobs: f.jobs}, nil
}

// countingSkillsClient counts GetSkillsByIds calls — the assertion this
// test exists for — and, for completeness, would fail any test that
// somehow still exercised the Phase 1b ListSkills fallback (it's left
// unimplemented; embedding skillsv1.SkillsServiceClient makes a call to it
// panic with a nil-pointer dereference rather than silently succeeding,
// which is exactly what should happen if dataloader.Middleware weren't
// actually wired in).
type countingSkillsClient struct {
	skillsv1.SkillsServiceClient
	callCount int32
}

// GetSkillsByIds's name (not GetSkillsByIDs) is dictated by the generated
// skillsv1.SkillsServiceClient interface (skills.proto's rpc is literally
// named GetSkillsByIds) — this implements that interface, so the name
// isn't a stylistic choice available to change here.
//
//nolint:revive // must match the generated interface method name exactly
func (c *countingSkillsClient) GetSkillsByIds(_ context.Context, req *skillsv1.GetSkillsByIdsRequest, _ ...grpc.CallOption) (*skillsv1.GetSkillsByIdsResponse, error) {
	atomic.AddInt32(&c.callCount, 1)
	out := make([]*skillsv1.Skill, 0, len(req.GetIds()))
	for _, id := range req.GetIds() {
		out = append(out, &skillsv1.Skill{Id: id, Name: "name-" + id, Category: "cat"})
	}
	return &skillsv1.GetSkillsByIdsResponse{Skills: out}, nil
}

// TestJobsQuery_RequiredSkills_BatchesThroughLiveHandler drives the actual
// gqlgen HTTP handler (the same construction cmd/api-gateway/main.go uses,
// wrapped in dataloader.Middleware the same way) with a real
// `jobs { requiredSkills { ... } }` query across 5 jobs sharing 4 distinct
// skill IDs, and asserts skills-service's GetSkillsByIds was invoked
// exactly once for the whole response — not once per job (5) and not once
// per requiredSkills field resolution.
func TestJobsQuery_RequiredSkills_BatchesThroughLiveHandler(t *testing.T) {
	jobSkillSets := [][]string{
		{"skill-1", "skill-2", "skill-3"},
		{"skill-2", "skill-3", "skill-4"},
		{"skill-1", "skill-3", "skill-4"},
		{"skill-1", "skill-2", "skill-4"},
		{"skill-1", "skill-2", "skill-3"},
	}
	jobs := make([]*jobsv1.Job, 0, len(jobSkillSets))
	for i, ids := range jobSkillSets {
		jobs = append(jobs, &jobsv1.Job{
			Id:               idFor(i),
			Title:            "job-" + idFor(i),
			Description:      "desc",
			RequiredSkillIds: ids,
		})
	}

	skillsClient := &countingSkillsClient{}
	resolver := &Resolver{
		JobsClient:   &fakeJobsClient{jobs: jobs},
		SkillsClient: skillsClient,
	}
	gqlHandler := handler.NewDefaultServer(generated.NewExecutableSchema(generated.Config{Resolvers: resolver}))
	wrapped := dataloader.Middleware(skillsClient)(gqlHandler)

	body, err := json.Marshal(map[string]string{
		Query: `{ jobs { id requiredSkills { id name category } } }`,
	})
	if err != nil {
		t.Fatalf("failed to marshal request body: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/query", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		wrapped.ServeHTTP(rec, req)
	}()
	wg.Wait()

	if rec.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Data struct {
			Jobs []struct {
				ID             string `json:"id"`
				RequiredSkills []struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"requiredSkills"`
			} `json:"jobs"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v; body: %s", err, rec.Body.String())
	}
	if len(resp.Errors) > 0 {
		t.Fatalf("expected no GraphQL errors, got: %+v", resp.Errors)
	}
	if len(resp.Data.Jobs) != len(jobs) {
		t.Fatalf("expected %d jobs in response, got %d", len(jobs), len(resp.Data.Jobs))
	}
	for i, j := range resp.Data.Jobs {
		if got, want := len(j.RequiredSkills), len(jobSkillSets[i]); got != want {
			t.Errorf("job %d: expected %d resolved requiredSkills, got %d", i, want, got)
		}
	}

	if got := atomic.LoadInt32(&skillsClient.callCount); got != 1 {
		t.Fatalf("expected exactly 1 batched GetSkillsByIds call across the whole response, got %d", got)
	}
}

func idFor(i int) string {
	return fmt.Sprintf("job-%d", i)
}

// Query is a tiny helper so the request-body literal above reads as
// `{Query: ...}` instead of a bare map key string repeated at both the
// marshal site and here — kept local to this test file since it's not a
// concept any production code needs.
const Query = "query"
