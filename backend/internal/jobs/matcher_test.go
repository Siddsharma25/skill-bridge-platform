package jobs

import (
	"context"
	"encoding/json"
	"testing"

	"go.uber.org/zap"

	kafkaplat "github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/kafka"
)

// fakeMatchStore is an in-memory stand-in for MatchStore. rows is keyed by
// (job_id, user_id) exactly like the real jobs.job_matches primary key —
// upsertCalls separately counts every UpsertMatch invocation, so a test
// can distinguish "upserted twice, one row" (correct, idempotent) from
// "upserted once" (would mean the handler didn't actually reprocess).
type fakeMatchStore struct {
	candidates  map[string]int
	upsertCalls int
	rows        map[[2]string]JobMatch
}

func newFakeMatchStore(candidates map[string]int) *fakeMatchStore {
	return &fakeMatchStore{candidates: candidates, rows: map[[2]string]JobMatch{}}
}

func (f *fakeMatchStore) CandidatesForSkills(_ context.Context, requiredSkillIDs []string) (map[string]int, error) {
	if len(requiredSkillIDs) == 0 {
		return map[string]int{}, nil
	}
	return f.candidates, nil
}

func (f *fakeMatchStore) UpsertMatch(_ context.Context, jobID, userID string, score float64) error {
	f.upsertCalls++
	f.rows[[2]string{jobID, userID}] = JobMatch{JobID: jobID, UserID: userID, Score: score}
	return nil
}

// TestHandleJobPosted_ReprocessingSameEventIsIdempotent is the test the
// task specifically calls for: the same job.posted event processed twice
// must result in exactly one job_matches row per matched user, not two —
// Kafka delivery is at-least-once, and UpsertMatch's ON CONFLICT (job_id,
// user_id) DO UPDATE is what makes that safe (see matcher.go).
func TestHandleJobPosted_ReprocessingSameEventIsIdempotent(t *testing.T) {
	// user-1 has both required skills (overlap 2/2), user-2 has one
	// (overlap 1/2, still clears matchThresholdOverlap=1), user-3 has
	// none and never appears in the candidates map at all (see
	// CandidatesForSkills' doc comment).
	store := newFakeMatchStore(map[string]int{"user-1": 2, "user-2": 1})
	publisher := &fakePublisher{}
	handler := HandleJobPosted(store, publisher, zap.NewNop())

	evt := kafkaplat.JobPosted{JobID: "job-1", RequiredSkillIDs: []string{"skill-1", "skill-2"}}
	payload, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("failed to marshal event: %v", err)
	}
	msg := kafkaplat.Message{Value: payload}

	if err := handler(context.Background(), msg); err != nil {
		t.Fatalf("first processing failed: %v", err)
	}
	if err := handler(context.Background(), msg); err != nil {
		t.Fatalf("second processing failed: %v", err)
	}

	if store.upsertCalls != 4 {
		t.Fatalf("expected UpsertMatch to be called 4 times (2 matched users x 2 deliveries), got %d", store.upsertCalls)
	}
	if len(store.rows) != 2 {
		t.Fatalf("expected exactly 1 job_matches row per matched user after reprocessing (2 total), not duplicated, got %d: %+v", len(store.rows), store.rows)
	}
	row1, ok := store.rows[[2]string{"job-1", "user-1"}]
	if !ok || row1.Score != 1.0 {
		t.Fatalf("expected user-1's row with score 1.0 (2/2 overlap), got %+v (present=%v)", row1, ok)
	}
	row2, ok := store.rows[[2]string{"job-1", "user-2"}]
	if !ok || row2.Score != 0.5 {
		t.Fatalf("expected user-2's row with score 0.5 (1/2 overlap), got %+v (present=%v)", row2, ok)
	}
	if _, ok := store.rows[[2]string{"job-1", "user-3"}]; ok {
		t.Fatalf("user-3 (zero overlap) must not have a match row")
	}

	// job.matched should have been published once per matched user per
	// delivery (4 total: 2 users x 2 deliveries) — this codebase doesn't
	// deduplicate the *event* itself, only the persisted row; a
	// downstream consumer of job.matched is expected to be idempotent
	// too, same as every other consumer in this codebase.
	if len(publisher.calls) != 4 {
		t.Fatalf("expected 4 job.matched publishes (2 matched users x 2 deliveries), got %d", len(publisher.calls))
	}
}

func TestHandleJobPosted_NoRequiredSkillsSkipsMatching(t *testing.T) {
	store := newFakeMatchStore(map[string]int{"user-1": 5})
	handler := HandleJobPosted(store, nil, zap.NewNop())

	evt := kafkaplat.JobPosted{JobID: "job-1", RequiredSkillIDs: nil}
	payload, _ := json.Marshal(evt)
	if err := handler(context.Background(), kafkaplat.Message{Value: payload}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.upsertCalls != 0 {
		t.Fatalf("expected no matching work for a job with no required skills, got %d upserts", store.upsertCalls)
	}
}

func TestHandleJobPosted_MalformedPayloadIsSkippedNotErrored(t *testing.T) {
	store := newFakeMatchStore(nil)
	handler := HandleJobPosted(store, nil, zap.NewNop())
	if err := handler(context.Background(), kafkaplat.Message{Value: []byte("not json")}); err != nil {
		t.Fatalf("expected a malformed payload to be logged and skipped, not returned as an error, got: %v", err)
	}
}

func TestHandleJobPosted_NoPublisherStillUpserts(t *testing.T) {
	store := newFakeMatchStore(map[string]int{"user-1": 1})
	handler := HandleJobPosted(store, nil, zap.NewNop())

	evt := kafkaplat.JobPosted{JobID: "job-1", RequiredSkillIDs: []string{"skill-1"}}
	payload, _ := json.Marshal(evt)
	if err := handler(context.Background(), kafkaplat.Message{Value: payload}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.upsertCalls != 1 {
		t.Fatalf("expected the match row to still be upserted with a nil publisher, got %d upserts", store.upsertCalls)
	}
}
