package jobs

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	jobsv1 "github.com/Siddsharma25/skill-bridge-platform/backend/gen/jobs/v1"
)

// fakeCache mirrors internal/skills/server_test.go's — a trivial in-memory
// stand-in for cache.Cache, no real Redis needed.
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

// TestListJobs_CacheHitShortCircuitsDatabase proves a cache hit actually
// skips the database, same reasoning/structure as
// internal/skills/server_test.go's equivalent — fetchAllJobs is swapped
// for a call-counting fake; with db left nil, any accidental fall-through
// to the real path would return Unavailable instead of the canned data.
func TestListJobs_CacheHitShortCircuitsDatabase(t *testing.T) {
	fc := newFakeCache()
	s := NewServer(nil, fc, zap.NewNop())

	fetchCalls := 0
	canned := []jobWithSkills{{Job: Job{ID: "job-1", Title: "Backend Engineer"}, Skills: []string{"skill-1"}}}
	s.fetchAllJobs = func(_ context.Context) ([]jobWithSkills, error) {
		fetchCalls++
		return canned, nil
	}

	resp1, err := s.ListJobs(context.Background(), &jobsv1.ListJobsRequest{})
	if err != nil {
		t.Fatalf("first ListJobs call failed: %v", err)
	}
	if len(resp1.GetJobs()) != 1 || resp1.GetJobs()[0].GetTitle() != "Backend Engineer" {
		t.Fatalf("unexpected first response: %+v", resp1)
	}
	if fetchCalls != 1 {
		t.Fatalf("expected 1 database fetch after first call, got %d", fetchCalls)
	}

	resp2, err := s.ListJobs(context.Background(), &jobsv1.ListJobsRequest{})
	if err != nil {
		t.Fatalf("second ListJobs call failed: %v", err)
	}
	if len(resp2.GetJobs()) != 1 || resp2.GetJobs()[0].GetTitle() != "Backend Engineer" {
		t.Fatalf("unexpected second response: %+v", resp2)
	}
	if fetchCalls != 1 {
		t.Fatalf("expected database fetch to still be called exactly once across two identical calls, got %d", fetchCalls)
	}
}

func TestListJobs_CacheMissFallsThroughToDatabase(t *testing.T) {
	s := NewServer(nil, newFakeCache(), zap.NewNop())
	_, err := s.ListJobs(context.Background(), &jobsv1.ListJobsRequest{})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("expected Unavailable on a cache miss with no database configured, got %v", err)
	}
}

// TestGetJob_CacheHitShortCircuitsDatabase is GetJob's equivalent of the
// ListJobs proof above, keyed per job ID.
func TestGetJob_CacheHitShortCircuitsDatabase(t *testing.T) {
	fc := newFakeCache()
	s := NewServer(nil, fc, zap.NewNop())

	fetchCalls := 0
	s.fetchJobByID = func(_ context.Context, id string) (*jobWithSkills, error) {
		fetchCalls++
		return &jobWithSkills{Job: Job{ID: id, Title: "Data Engineer"}}, nil
	}

	for i := 0; i < 2; i++ {
		resp, err := s.GetJob(context.Background(), &jobsv1.GetJobRequest{Id: "job-7"})
		if err != nil {
			t.Fatalf("GetJob call %d failed: %v", i+1, err)
		}
		if resp.GetJob().GetTitle() != "Data Engineer" {
			t.Fatalf("unexpected response on call %d: %+v", i+1, resp)
		}
	}
	if fetchCalls != 1 {
		t.Fatalf("expected database fetch to be called exactly once across two identical GetJob calls, got %d", fetchCalls)
	}
}

// TestCreateJob_InvalidatesListCache proves the write-through half: after
// ListJobs has populated jobs:all, CreateJob must DEL it in the same write
// path.
func TestCreateJob_InvalidatesListCache(t *testing.T) {
	fc := newFakeCache()
	s := NewServer(nil, fc, zap.NewNop())
	s.fetchAllJobs = func(_ context.Context) ([]jobWithSkills, error) {
		return []jobWithSkills{{Job: Job{ID: "job-1", Title: "Backend Engineer"}}}, nil
	}
	s.insertJob = func(_ context.Context, job *Job, _ []string) error {
		job.ID = "job-2"
		return nil
	}

	if _, err := s.ListJobs(context.Background(), &jobsv1.ListJobsRequest{}); err != nil {
		t.Fatalf("priming ListJobs failed: %v", err)
	}
	if _, ok := fc.Get(context.Background(), jobsAllCacheKey); !ok {
		t.Fatalf("expected jobs:all to be populated after ListJobs")
	}

	if _, err := s.CreateJob(context.Background(), &jobsv1.CreateJobRequest{Title: "Frontend Engineer"}); err != nil {
		t.Fatalf("CreateJob failed: %v", err)
	}
	if _, ok := fc.Get(context.Background(), jobsAllCacheKey); ok {
		t.Fatalf("expected jobs:all to be deleted after CreateJob, but it's still present")
	}
}

func TestCreateJob_RejectsMissingTitle(t *testing.T) {
	s := NewServer(nil, newFakeCache(), zap.NewNop())
	_, err := s.CreateJob(context.Background(), &jobsv1.CreateJobRequest{Title: ""})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
}

func TestCreateJob_NoDBReturnsUnavailable(t *testing.T) {
	s := NewServer(nil, newFakeCache(), zap.NewNop())
	_, err := s.CreateJob(context.Background(), &jobsv1.CreateJobRequest{Title: "Backend Engineer"})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("expected Unavailable when db is nil, got %v", err)
	}
}

func TestGetJob_NoDBReturnsUnavailable(t *testing.T) {
	s := NewServer(nil, newFakeCache(), zap.NewNop())
	_, err := s.GetJob(context.Background(), &jobsv1.GetJobRequest{Id: "job-1"})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("expected Unavailable when db is nil, got %v", err)
	}
}
