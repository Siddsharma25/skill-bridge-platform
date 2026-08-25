package jobs

import (
	"context"
	"encoding/json"
	"testing"

	"go.uber.org/zap"

	kafkaplat "github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/kafka"
)

// fakeSnapshotStore is an in-memory stand-in for SnapshotStore — mirrors
// gormSnapshotStore's "delete-then-insert as one replace" semantics
// without a real database, letting this test prove idempotent
// reprocessing at the logic level (the real gormSnapshotStore does the
// equivalent replace inside a Postgres transaction; that transactional
// behavior itself is exercised by this phase's live verification against
// a real database, not by go test — see docs/DECISIONS.md and
// backend/Makefile's "no live DB required" convention for `go test`).
type fakeSnapshotStore struct {
	// replaceCalls counts how many times ReplaceUserSkills was invoked,
	// so a test can distinguish "replaced twice, same end state" from
	// "only replaced once" — both matter for the idempotency proof below.
	replaceCalls int
	byUser       map[string][]UserSkillSnapshot
}

func newFakeSnapshotStore() *fakeSnapshotStore {
	return &fakeSnapshotStore{byUser: map[string][]UserSkillSnapshot{}}
}

func (f *fakeSnapshotStore) ReplaceUserSkills(_ context.Context, userID string, skills []UserSkillSnapshot) error {
	f.replaceCalls++
	f.byUser[userID] = skills
	return nil
}

// TestHandleUserSkillsUpdated_ReprocessingSameEventIsIdempotent is the
// test the task specifically calls for: processing the same
// user.skills.updated event twice must leave the snapshot correct, not
// duplicated. Kafka delivery is at-least-once, so a consumer that isn't
// idempotent here would double up a user's skill rows on every
// redelivery/consumer restart.
func TestHandleUserSkillsUpdated_ReprocessingSameEventIsIdempotent(t *testing.T) {
	store := newFakeSnapshotStore()
	handler := HandleUserSkillsUpdated(store, zap.NewNop())

	evt := kafkaplat.UserSkillsUpdated{
		UserID: "user-1",
		Skills: []kafkaplat.UserSkillsUpdatedSkill{
			{SkillID: "skill-1", Proficiency: "beginner"},
			{SkillID: "skill-2", Proficiency: "expert"},
		},
	}
	payload, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("failed to marshal event: %v", err)
	}
	msg := kafkaplat.Message{Topic: kafkaplat.TopicUserSkillsUpdated, Key: []byte("user-1"), Value: payload}

	// Process the same event twice — simulating at-least-once redelivery
	// or a consumer restart before the offset committed.
	if err := handler(context.Background(), msg); err != nil {
		t.Fatalf("first processing failed: %v", err)
	}
	if err := handler(context.Background(), msg); err != nil {
		t.Fatalf("second processing failed: %v", err)
	}

	if store.replaceCalls != 2 {
		t.Fatalf("expected ReplaceUserSkills to be invoked twice (once per delivery), got %d", store.replaceCalls)
	}
	rows := store.byUser["user-1"]
	if len(rows) != 2 {
		t.Fatalf("expected exactly 2 snapshot rows after reprocessing the same event twice (not duplicated to 4), got %d: %+v", len(rows), rows)
	}
	seen := map[string]bool{}
	for _, r := range rows {
		if seen[r.SkillID] {
			t.Fatalf("duplicate skill_id %q in snapshot after reprocessing", r.SkillID)
		}
		seen[r.SkillID] = true
	}
}

// TestHandleUserSkillsUpdated_NewerEventFullyReplacesOlderOne proves the
// "full replace, not a delta merge" contract: a user who had skill-1 and
// skill-2, then sends a new event with only skill-2, must end up with
// exactly skill-2 in the snapshot — skill-1 must be gone, not merged in
// alongside the new list.
func TestHandleUserSkillsUpdated_NewerEventFullyReplacesOlderOne(t *testing.T) {
	store := newFakeSnapshotStore()
	handler := HandleUserSkillsUpdated(store, zap.NewNop())

	first := kafkaplat.UserSkillsUpdated{
		UserID: "user-1",
		Skills: []kafkaplat.UserSkillsUpdatedSkill{
			{SkillID: "skill-1", Proficiency: "beginner"},
			{SkillID: "skill-2", Proficiency: "expert"},
		},
	}
	firstPayload, _ := json.Marshal(first)
	if err := handler(context.Background(), kafkaplat.Message{Value: firstPayload}); err != nil {
		t.Fatalf("first processing failed: %v", err)
	}

	second := kafkaplat.UserSkillsUpdated{
		UserID: "user-1",
		Skills: []kafkaplat.UserSkillsUpdatedSkill{
			{SkillID: "skill-2", Proficiency: "expert"},
		},
	}
	secondPayload, _ := json.Marshal(second)
	if err := handler(context.Background(), kafkaplat.Message{Value: secondPayload}); err != nil {
		t.Fatalf("second processing failed: %v", err)
	}

	rows := store.byUser["user-1"]
	if len(rows) != 1 || rows[0].SkillID != "skill-2" {
		t.Fatalf("expected the snapshot to be fully replaced with just skill-2, got %+v", rows)
	}
}

func TestHandleUserSkillsUpdated_MalformedPayloadIsSkippedNotErrored(t *testing.T) {
	store := newFakeSnapshotStore()
	handler := HandleUserSkillsUpdated(store, zap.NewNop())

	if err := handler(context.Background(), kafkaplat.Message{Value: []byte("not json")}); err != nil {
		t.Fatalf("expected a malformed payload to be logged and skipped, not returned as an error, got: %v", err)
	}
	if store.replaceCalls != 0 {
		t.Fatalf("expected no store call for a malformed payload, got %d", store.replaceCalls)
	}
}
