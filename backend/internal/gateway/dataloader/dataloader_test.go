package dataloader

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc"

	skillsv1 "github.com/Siddsharma25/skill-bridge-platform/backend/gen/skills/v1"
)

// fakeSkillsClient counts GetSkillsByIds calls and records the IDs each
// call received, so the test below can assert on both the call count and
// on which IDs got batched together — a call count of 1 alone wouldn't
// rule out, say, silently dropping half the requested IDs.
type fakeSkillsClient struct {
	skillsv1.SkillsServiceClient // embed for the methods this test doesn't use

	mu        sync.Mutex
	callCount int32
	callArgs  [][]string
}

//
//nolint:revive // must match the generated skillsv1.SkillsServiceClient interface method name (skills.proto's rpc is literally named GetSkillsByIds)
func (f *fakeSkillsClient) GetSkillsByIds(_ context.Context, req *skillsv1.GetSkillsByIdsRequest, _ ...grpc.CallOption) (*skillsv1.GetSkillsByIdsResponse, error) {
	atomic.AddInt32(&f.callCount, 1)
	f.mu.Lock()
	f.callArgs = append(f.callArgs, append([]string(nil), req.GetIds()...))
	f.mu.Unlock()

	out := make([]*skillsv1.Skill, 0, len(req.GetIds()))
	for _, id := range req.GetIds() {
		out = append(out, &skillsv1.Skill{Id: id, Name: "name-" + id, Category: "cat"})
	}
	return &skillsv1.GetSkillsByIdsResponse{Skills: out}, nil
}

// TestBatchGetSkillsByID_BatchesConcurrentLoadsIntoOneCall is the proof the
// task specifically calls for: N "jobs" each needing several overlapping
// skill IDs must produce exactly one batched GetSkillsByIds call to
// skills-service, not one call per job/skill. Simulating gqlgen's
// concurrent field resolution with goroutines issuing Load() calls at the
// same time is the standard way to exercise graph-gophers/dataloader's
// wait-window batching deterministically in a test — see waitWindow's doc
// comment in dataloader.go.
func TestBatchGetSkillsByID_BatchesConcurrentLoadsIntoOneCall(t *testing.T) {
	fake := &fakeSkillsClient{}
	loaders := &Loaders{
		SkillByID: newTestLoader(fake),
	}

	// Simulate 5 "jobs," each requiring 3 skill IDs drawn from a pool of 4
	// distinct skills (skill-1..skill-4) — deliberately overlapping, the
	// same way real job postings would share common skills like "Go" or
	// "SQL". That's 15 Load() calls total across only 4 distinct IDs.
	jobRequiredSkillIDs := [][]string{
		{"skill-1", "skill-2", "skill-3"},
		{"skill-2", "skill-3", "skill-4"},
		{"skill-1", "skill-3", "skill-4"},
		{"skill-1", "skill-2", "skill-4"},
		{"skill-1", "skill-2", "skill-3"},
	}

	var wg sync.WaitGroup
	results := make(chan *dlSkillResult, 15)

	for _, ids := range jobRequiredSkillIDs {
		for _, id := range ids {
			wg.Add(1)
			go func(id string) {
				defer wg.Done()
				thunk := loaders.SkillByID.Load(context.Background(), id)
				sk, err := thunk()
				results <- &dlSkillResult{id: id, err: err, found: sk != nil}
			}(id)
		}
	}
	wg.Wait()
	close(results)

	for r := range results {
		if r.err != nil {
			t.Fatalf("Load(%q) returned unexpected error: %v", r.id, r.err)
		}
		if !r.found {
			t.Fatalf("Load(%q) resolved to nil, expected a skill", r.id)
		}
	}

	if got := atomic.LoadInt32(&fake.callCount); got != 1 {
		t.Fatalf("expected exactly 1 batched GetSkillsByIds call, got %d (calls: %v)", got, fake.callArgs)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	gotIDs := map[string]bool{}
	for _, id := range fake.callArgs[0] {
		gotIDs[id] = true
	}
	for _, want := range []string{"skill-1", "skill-2", "skill-3", "skill-4"} {
		if !gotIDs[want] {
			t.Errorf("expected the single batched call to include %q, got %v", want, fake.callArgs[0])
		}
	}
}

type dlSkillResult struct {
	id    string
	err   error
	found bool
}

// newTestLoader builds a Loader identical to the one Middleware
// constructs, so the test exercises the exact same batch function and
// wait-window configuration production code uses.
func newTestLoader(client skillsv1.SkillsServiceClient) *loaderT {
	return newBatchedLoaderForTest(client)
}
