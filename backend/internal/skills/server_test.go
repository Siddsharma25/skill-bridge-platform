package skills

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	skillsv1 "github.com/Siddsharma25/skill-bridge-platform/backend/gen/skills/v1"
)

// fakeCache is a map-based in-memory stand-in for cache.Cache — no real
// Redis needed to prove the write-through caching behavior below. It's
// intentionally trivial (no TTL expiry simulated) since these tests only
// care about hit/miss and invalidation, not timing.
type fakeCache struct {
	data map[string]string
}

func newFakeCache() *fakeCache { return &fakeCache{data: map[string]string{}} }

func (f *fakeCache) Get(_ context.Context, key string) (string, bool) {
	v, ok := f.data[key]
	return v, ok
}

func (f *fakeCache) Set(_ context.Context, key, value string, _ time.Duration) {
	f.data[key] = value
}

func (f *fakeCache) Del(_ context.Context, keys ...string) {
	for _, k := range keys {
		delete(f.data, k)
	}
}

// TestListSkills_CacheHitShortCircuitsDatabase is the test the task
// specifically calls for: prove a cache hit actually skips the database,
// not just "trust it because the code looks right." fetchAllSkills is
// swapped for a call-counting fake (see server.go's seam comment) — with
// db left nil, any accidental fall-through to the real database path would
// return an Unavailable error instead of the canned data, so a passing
// second call is only possible if the cache hit truly short-circuited it.
func TestListSkills_CacheHitShortCircuitsDatabase(t *testing.T) {
	fc := newFakeCache()
	s := NewServer(nil, fc, zap.NewNop())

	fetchCalls := 0
	canned := []Skill{{ID: "1", Name: "Go", Category: "Languages"}}
	s.fetchAllSkills = func(_ context.Context) ([]Skill, error) {
		fetchCalls++
		return canned, nil
	}

	// First call: cache miss, must hit the (fake) database.
	resp1, err := s.ListSkills(context.Background(), &skillsv1.ListSkillsRequest{})
	if err != nil {
		t.Fatalf("first ListSkills call failed: %v", err)
	}
	if len(resp1.GetSkills()) != 1 || resp1.GetSkills()[0].GetName() != "Go" {
		t.Fatalf("unexpected first response: %+v", resp1)
	}
	if fetchCalls != 1 {
		t.Fatalf("expected 1 database fetch after first call, got %d", fetchCalls)
	}

	// Second call: identical request, should be served from cache without
	// touching fetchAllSkills again.
	resp2, err := s.ListSkills(context.Background(), &skillsv1.ListSkillsRequest{})
	if err != nil {
		t.Fatalf("second ListSkills call failed: %v", err)
	}
	if len(resp2.GetSkills()) != 1 || resp2.GetSkills()[0].GetName() != "Go" {
		t.Fatalf("unexpected second response: %+v", resp2)
	}
	if fetchCalls != 1 {
		t.Fatalf("expected database fetch to still be called exactly once across two identical calls, got %d", fetchCalls)
	}
}

// TestListSkills_CacheMissFallsThroughToDatabase proves the inverse: with
// no cache entry present and db nil, ListSkills must actually attempt the
// database path (surfacing Unavailable), not silently succeed with empty
// data — i.e. the cache-hit shortcut in the test above isn't just always
// returning early regardless of whether anything is cached.
func TestListSkills_CacheMissFallsThroughToDatabase(t *testing.T) {
	s := NewServer(nil, newFakeCache(), zap.NewNop())
	_, err := s.ListSkills(context.Background(), &skillsv1.ListSkillsRequest{})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("expected Unavailable on a cache miss with no database configured, got %v", err)
	}
}

// TestCreateSkill_InvalidatesCache proves the write-through half of
// write-through caching: after ListSkills has populated skills:all,
// CreateSkill must DEL it in the same write path, so a subsequent
// ListSkills call is forced to re-fetch (and would see the new skill in
// production; here it just proves the cache entry is gone).
func TestCreateSkill_InvalidatesCache(t *testing.T) {
	fc := newFakeCache()
	s := NewServer(nil, fc, zap.NewNop())
	s.fetchAllSkills = func(_ context.Context) ([]Skill, error) {
		return []Skill{{ID: "1", Name: "Go", Category: "Languages"}}, nil
	}
	s.insertSkill = func(_ context.Context, skill *Skill) error {
		skill.ID = "2"
		return nil
	}

	if _, err := s.ListSkills(context.Background(), &skillsv1.ListSkillsRequest{}); err != nil {
		t.Fatalf("priming ListSkills failed: %v", err)
	}
	if _, ok := fc.Get(context.Background(), skillsAllCacheKey); !ok {
		t.Fatalf("expected skills:all to be populated after ListSkills")
	}

	if _, err := s.CreateSkill(context.Background(), &skillsv1.CreateSkillRequest{Name: "Rust", Category: "Languages"}); err != nil {
		t.Fatalf("CreateSkill failed: %v", err)
	}
	if _, ok := fc.Get(context.Background(), skillsAllCacheKey); ok {
		t.Fatalf("expected skills:all to be deleted after CreateSkill, but it's still present")
	}
}

func TestCreateSkill_RejectsMissingFields(t *testing.T) {
	s := NewServer(nil, newFakeCache(), zap.NewNop())
	_, err := s.CreateSkill(context.Background(), &skillsv1.CreateSkillRequest{Name: "", Category: ""})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
}

func TestCreateSkill_NoDBReturnsUnavailable(t *testing.T) {
	s := NewServer(nil, newFakeCache(), zap.NewNop())
	_, err := s.CreateSkill(context.Background(), &skillsv1.CreateSkillRequest{Name: "Go", Category: "Languages"})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("expected Unavailable when db is nil, got %v", err)
	}
}
