package users

import (
	"context"
	"encoding/json"
	"testing"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	usersv1 "github.com/Siddsharma25/skill-bridge-platform/backend/gen/users/v1"
	kafkaplat "github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/kafka"
)

// fakePublisher is a trivial in-memory stand-in for kafka.Publisher —
// same shape as internal/skills/server_test.go's — capturing every
// Publish call so a test can assert an event was published exactly once,
// with the expected topic/key/payload, without a real Kafka broker.
type fakePublisher struct {
	calls []publishCall
}

type publishCall struct {
	topic, key string
	value      []byte
}

func (f *fakePublisher) Publish(_ context.Context, topic, key string, value []byte) {
	f.calls = append(f.calls, publishCall{topic: topic, key: key, value: value})
}

func newTestServer(publisher kafkaplat.Publisher) *Server {
	// db is left nil throughout: every test below swaps in fakes for
	// listSkillRows/upsertSkillRow (the same seam-for-testability pattern
	// internal/jobs and internal/skills already use), so nothing here
	// needs a real database.
	s := NewServer(nil, publisher, zap.NewNop())
	return s
}

func TestAddUserSkill_RejectsMissingFields(t *testing.T) {
	s := newTestServer(nil)
	_, err := s.AddUserSkill(context.Background(), &usersv1.AddUserSkillRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
}

func TestAddUserSkill_NoDBReturnsUnavailable(t *testing.T) {
	s := newTestServer(nil)
	_, err := s.AddUserSkill(context.Background(), &usersv1.AddUserSkillRequest{
		UserId: "user-1", SkillId: "skill-1", Proficiency: "expert",
	})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("expected Unavailable when db is nil, got %v", err)
	}
}

// TestAddUserSkill_PublishesFullSkillListEvent is the Phase 2 proof: a
// successful AddUserSkill publishes the user's *entire* current skill
// list (not just the one skill that changed) to user.skills.updated,
// since jobs-service's snapshot consumer treats each event as
// authoritative and replaces its whole projection for that user — see
// docs/DECISIONS.md.
func TestAddUserSkill_PublishesFullSkillListEvent(t *testing.T) {
	fp := &fakePublisher{}
	s := newTestServer(fp)

	upserted := false
	s.upsertSkillRow = func(_ context.Context, row *UserSkill) error {
		upserted = true
		if row.UserID != "user-1" || row.SkillID != "skill-2" || row.Proficiency != "intermediate" {
			t.Fatalf("unexpected row passed to upsertSkillRow: %+v", row)
		}
		return nil
	}
	// The user already had skill-1; after adding skill-2, the full
	// current list (both) is what must be published — not a one-entry
	// delta for just skill-2.
	s.listSkillRows = func(_ context.Context, userID string) ([]UserSkill, error) {
		if userID != "user-1" {
			t.Fatalf("unexpected userID passed to listSkillRows: %q", userID)
		}
		return []UserSkill{
			{UserID: "user-1", SkillID: "skill-1", Proficiency: "beginner"},
			{UserID: "user-1", SkillID: "skill-2", Proficiency: "intermediate"},
		}, nil
	}

	_, err := s.AddUserSkill(context.Background(), &usersv1.AddUserSkillRequest{
		UserId: "user-1", SkillId: "skill-2", Proficiency: "intermediate",
	})
	if err != nil {
		t.Fatalf("AddUserSkill failed: %v", err)
	}
	if !upserted {
		t.Fatalf("expected upsertSkillRow to be called")
	}

	if len(fp.calls) != 1 {
		t.Fatalf("expected exactly 1 published event, got %d", len(fp.calls))
	}
	call := fp.calls[0]
	if call.topic != kafkaplat.TopicUserSkillsUpdated {
		t.Fatalf("expected topic %q, got %q", kafkaplat.TopicUserSkillsUpdated, call.topic)
	}
	if call.key != "user-1" {
		t.Fatalf("expected key %q (keyed by user_id), got %q", "user-1", call.key)
	}

	var evt kafkaplat.UserSkillsUpdated
	if err := json.Unmarshal(call.value, &evt); err != nil {
		t.Fatalf("failed to decode published payload: %v", err)
	}
	if evt.UserID != "user-1" {
		t.Fatalf("expected user_id %q in payload, got %q", "user-1", evt.UserID)
	}
	if len(evt.Skills) != 2 {
		t.Fatalf("expected the full 2-skill list in the payload (not a 1-skill delta), got %d skills: %+v", len(evt.Skills), evt.Skills)
	}
}

// TestAddUserSkill_NoPublisherStillSucceeds proves AddUserSkill works with
// a nil publisher (Kafka not configured on this instance) — publishing is
// best-effort, never a correctness dependency for the primary write.
func TestAddUserSkill_NoPublisherStillSucceeds(t *testing.T) {
	s := newTestServer(nil)
	s.upsertSkillRow = func(_ context.Context, _ *UserSkill) error { return nil }
	_, err := s.AddUserSkill(context.Background(), &usersv1.AddUserSkillRequest{
		UserId: "user-1", SkillId: "skill-1", Proficiency: "expert",
	})
	if err != nil {
		t.Fatalf("AddUserSkill failed with nil publisher: %v", err)
	}
}

// TestAddUserSkill_PublishFailureDoesNotFailRPC proves that even when
// fetching the updated skill list fails (the only failure mode on this
// path, since Publish itself never returns an error to the caller — see
// kafka.Producer.Publish), AddUserSkill still reports success: the
// primary write already succeeded, and the event is best-effort.
func TestAddUserSkill_PublishFailureDoesNotFailRPC(t *testing.T) {
	fp := &fakePublisher{}
	s := newTestServer(fp)
	s.upsertSkillRow = func(_ context.Context, _ *UserSkill) error { return nil }
	s.listSkillRows = func(_ context.Context, _ string) ([]UserSkill, error) {
		return nil, context.DeadlineExceeded
	}

	_, err := s.AddUserSkill(context.Background(), &usersv1.AddUserSkillRequest{
		UserId: "user-1", SkillId: "skill-1", Proficiency: "expert",
	})
	if err != nil {
		t.Fatalf("AddUserSkill must not fail when the post-write event fetch fails, got: %v", err)
	}
	if len(fp.calls) != 0 {
		t.Fatalf("expected no event published when the skill list fetch failed, got %d", len(fp.calls))
	}
}

func TestListUserSkills_RejectsMissingUserID(t *testing.T) {
	s := newTestServer(nil)
	_, err := s.ListUserSkills(context.Background(), &usersv1.ListUserSkillsRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
}

func TestListUserSkills_NoDBReturnsUnavailable(t *testing.T) {
	s := newTestServer(nil)
	_, err := s.ListUserSkills(context.Background(), &usersv1.ListUserSkillsRequest{UserId: "user-1"})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("expected Unavailable when db is nil, got %v", err)
	}
}
