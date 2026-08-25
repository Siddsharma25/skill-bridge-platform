package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	jobsv1 "github.com/Siddsharma25/skill-bridge-platform/backend/gen/jobs/v1"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/cache"
	kafkaplat "github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/kafka"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/logger"
)

// jobsAllCacheKey caches ListJobs' full result; jobCacheKey caches one
// GetJob result per job ID. jobsCacheTTL is deliberately short (unlike
// skills-service's 1h) — job postings are more dynamic and, unlike
// skills-service, there's no per-job invalidation on write here (only
// CreateJob exists; there's no UpdateJob yet), so a short TTL bounds
// staleness instead of an explicit DEL covering every case. See
// docs/DECISIONS.md.
const (
	jobsAllCacheKey = "jobs:all"
	jobsCacheTTL    = 30 * time.Second
)

func jobCacheKey(id string) string { return "jobs:" + id }

// Server implements jobsv1.JobsServiceServer. db may be nil — same
// degraded-start pattern as every other service in this codebase (see
// backend/CLAUDE.md). cache may also be nil/disabled (see
// internal/platform/cache) — caching is purely an optimization.
type Server struct {
	jobsv1.UnimplementedJobsServiceServer

	db        *gorm.DB
	cache     cache.Cache
	publisher kafkaplat.Publisher
	log       *zap.Logger

	// fetchAllJobs, fetchJobByID, and insertJob default to thin wrappers
	// over s.db but are swappable fields, same seam-for-testability
	// reasoning as internal/skills/server.go — see that file's comment and
	// server_test.go for why.
	fetchAllJobs func(ctx context.Context) ([]jobWithSkills, error)
	fetchJobByID func(ctx context.Context, id string) (*jobWithSkills, error)
	insertJob    func(ctx context.Context, job *Job, skillIDs []string) error
	// fetchJobMatches defaults to a thin wrapper over s.db (see
	// queryJobMatchesFromDB below), same seam pattern, for
	// ListJobMatches.
	fetchJobMatches func(ctx context.Context, jobID string) ([]JobMatch, error)
}

// jobWithSkills bundles a Job with its resolved required-skill IDs — the
// shape both the cache and the proto conversion need together.
type jobWithSkills struct {
	Job    Job
	Skills []string
}

// NewServer constructs a Server. log must not be nil; db, c, and
// publisher may all be nil (a nil publisher simply means CreateJob skips
// publishing job.posted — checked explicitly at that call site, same
// pattern as skills-service/users-service; see docs/DECISIONS.md's Phase
// 2 notes).
func NewServer(db *gorm.DB, c cache.Cache, publisher kafkaplat.Publisher, log *zap.Logger) *Server {
	s := &Server{db: db, cache: c, publisher: publisher, log: log}
	s.fetchAllJobs = s.queryAllJobsFromDB
	s.fetchJobByID = s.queryJobByIDFromDB
	s.insertJob = s.insertJobIntoDB
	s.fetchJobMatches = s.queryJobMatchesFromDB
	return s
}

func (s *Server) queryJobMatchesFromDB(ctx context.Context, jobID string) ([]JobMatch, error) {
	if s.db == nil {
		return nil, status.Error(codes.Unavailable, "database is not configured on this instance")
	}
	var rows []JobMatch
	if err := s.db.WithContext(ctx).Where("job_id = ?", jobID).Order("score DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *Server) queryAllJobsFromDB(ctx context.Context) ([]jobWithSkills, error) {
	if s.db == nil {
		return nil, status.Error(codes.Unavailable, "database is not configured on this instance")
	}
	var rows []Job
	if err := s.db.WithContext(ctx).Order("created_at DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	skillsByJob, err := s.requiredSkillsByJobID(ctx, jobIDs(rows))
	if err != nil {
		return nil, err
	}
	out := make([]jobWithSkills, len(rows))
	for i := range rows {
		out[i] = jobWithSkills{Job: rows[i], Skills: skillsByJob[rows[i].ID]}
	}
	return out, nil
}

func (s *Server) queryJobByIDFromDB(ctx context.Context, id string) (*jobWithSkills, error) {
	if s.db == nil {
		return nil, status.Error(codes.Unavailable, "database is not configured on this instance")
	}
	var job Job
	if err := s.db.WithContext(ctx).Where("id = ?", id).First(&job).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "job not found")
		}
		return nil, err
	}
	skillsByJob, err := s.requiredSkillsByJobID(ctx, []string{job.ID})
	if err != nil {
		return nil, err
	}
	return &jobWithSkills{Job: job, Skills: skillsByJob[job.ID]}, nil
}

func (s *Server) insertJobIntoDB(ctx context.Context, job *Job, skillIDs []string) error {
	if s.db == nil {
		return status.Error(codes.Unavailable, "database is not configured on this instance")
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(job).Error; err != nil {
			return err
		}
		if len(skillIDs) == 0 {
			return nil
		}
		required := make([]RequiredSkill, 0, len(skillIDs))
		for _, id := range skillIDs {
			required = append(required, RequiredSkill{JobID: job.ID, SkillID: id})
		}
		return tx.Create(&required).Error
	})
}

// CreateJob is unauthenticated in Phase 1b — same reasoning as
// SkillsService.CreateSkill (see docs/DECISIONS.md). The job row and its
// required-skill rows are inserted in one transaction so a partial write
// (job created, skills half-inserted) can't happen. Successfully creating
// a job DELs jobs:all (write-through, per docs/DECISIONS.md) so ListJobs
// never serves a stale list missing the new posting; there's no
// jobs:<id> to invalidate for a brand-new ID, so GetJob relies on
// jobsCacheTTL alone. It also publishes job.posted (Phase 2) — the
// trigger for the in-process matching worker (see matcher.go) to score
// every candidate in jobs.user_skill_snapshot against this job's required
// skills, entirely from consumed Kafka events, no synchronous call back
// into this RPC.
func (s *Server) CreateJob(ctx context.Context, req *jobsv1.CreateJobRequest) (*jobsv1.CreateJobResponse, error) {
	log := logger.FromContext(ctx, s.log)

	title := strings.TrimSpace(req.GetTitle())
	if title == "" {
		return nil, status.Error(codes.InvalidArgument, "title is required")
	}

	job := Job{
		ID:          uuid.NewString(),
		Title:       title,
		Description: strings.TrimSpace(req.GetDescription()),
	}
	skillIDs := dedupeNonEmpty(req.GetRequiredSkillIds())

	if err := s.insertJob(ctx, &job, skillIDs); err != nil {
		if _, isStatus := status.FromError(err); isStatus {
			return nil, err
		}
		log.Error("failed to create job", zap.Error(err))
		return nil, status.Error(codes.Internal, "failed to create job")
	}

	if s.cache != nil {
		s.cache.Del(ctx, jobsAllCacheKey)
	}

	if s.publisher != nil {
		payload, err := json.Marshal(kafkaplat.JobPosted{
			JobID:            job.ID,
			RequiredSkillIDs: skillIDs,
			PostedAt:         time.Now().UTC(),
		})
		if err != nil {
			log.Error("failed to marshal job.posted event; not published", zap.Error(err), zap.String("job_id", job.ID))
		} else {
			s.publisher.Publish(ctx, kafkaplat.TopicJobPosted, job.ID, payload)
		}
	}

	log.Info("created job", zap.String("job_id", job.ID), zap.String("title", job.Title))
	return &jobsv1.CreateJobResponse{Job: toProto(&job, skillIDs)}, nil
}

// ListJobMatches returns every jobs.job_matches row for job_id, highest
// score first — the matching worker's output (see matcher.go), produced
// entirely by consuming job.posted and the user_skill_snapshot
// projection, never a synchronous call back into this RPC's own
// CreateJob. Added so the Phase 2 checkpoint can be demonstrated through
// the gateway's GraphQL API — see docs/DECISIONS.md.
func (s *Server) ListJobMatches(ctx context.Context, req *jobsv1.ListJobMatchesRequest) (*jobsv1.ListJobMatchesResponse, error) {
	log := logger.FromContext(ctx, s.log)

	jobID := strings.TrimSpace(req.GetJobId())
	if jobID == "" {
		return nil, status.Error(codes.InvalidArgument, "job_id is required")
	}

	rows, err := s.fetchJobMatches(ctx, jobID)
	if err != nil {
		if _, isStatus := status.FromError(err); isStatus {
			return nil, err
		}
		log.Error("failed to list job matches", zap.Error(err), zap.String("job_id", jobID))
		return nil, status.Error(codes.Internal, "failed to list job matches")
	}

	out := make([]*jobsv1.JobMatch, 0, len(rows))
	for i := range rows {
		out = append(out, &jobsv1.JobMatch{
			UserId:    rows[i].UserID,
			Score:     rows[i].Score,
			MatchedAt: rows[i].MatchedAt.UTC().Format(time.RFC3339),
		})
	}
	return &jobsv1.ListJobMatchesResponse{Matches: out}, nil
}

// ListJobs returns every job posting, most-recently-created first, with
// required_skill_ids attached. Served from the jobs:all cache entry when
// present (short TTL — see jobsCacheTTL); on a miss, falls through to the
// database (which itself fetches every job's required skills in one
// additional query, grouped in memory by job_id, rather than per-job — a
// job's own required-skill IDs are jobs-service's own data, unlike the
// cross-service skill-name resolution the gateway leaves as a deliberate
// N+1, see jobs.proto) and populates the cache for next time.
func (s *Server) ListJobs(ctx context.Context, _ *jobsv1.ListJobsRequest) (*jobsv1.ListJobsResponse, error) {
	log := logger.FromContext(ctx, s.log)

	if s.cache != nil {
		if cached, ok := s.cache.Get(ctx, jobsAllCacheKey); ok {
			jobs, err := decodeCachedJobs(cached)
			if err == nil {
				return &jobsv1.ListJobsResponse{Jobs: jobs}, nil
			}
			log.Warn("failed to decode cached jobs; falling through to database", zap.Error(err))
		}
	}

	rows, err := s.fetchAllJobs(ctx)
	if err != nil {
		if _, isStatus := status.FromError(err); isStatus {
			return nil, err
		}
		log.Error("failed to list jobs", zap.Error(err))
		return nil, status.Error(codes.Internal, "failed to list jobs")
	}

	out := make([]*jobsv1.Job, 0, len(rows))
	for i := range rows {
		out = append(out, toProto(&rows[i].Job, rows[i].Skills))
	}

	if s.cache != nil {
		if encoded, err := encodeCachedJobs(rows); err == nil {
			s.cache.Set(ctx, jobsAllCacheKey, encoded, jobsCacheTTL)
		} else {
			log.Warn("failed to encode jobs for caching", zap.Error(err))
		}
	}

	return &jobsv1.ListJobsResponse{Jobs: out}, nil
}

// GetJob returns a single job posting by ID, or NotFound. Served from its
// jobs:<id> cache entry when present (short TTL, same reasoning as
// ListJobs); on a miss, falls through to the database and populates the
// cache for next time.
func (s *Server) GetJob(ctx context.Context, req *jobsv1.GetJobRequest) (*jobsv1.GetJobResponse, error) {
	log := logger.FromContext(ctx, s.log)

	id := strings.TrimSpace(req.GetId())
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	key := jobCacheKey(id)
	if s.cache != nil {
		if cached, ok := s.cache.Get(ctx, key); ok {
			job, err := decodeCachedJob(cached)
			if err == nil {
				return &jobsv1.GetJobResponse{Job: job}, nil
			}
			log.Warn("failed to decode cached job; falling through to database", zap.Error(err), zap.String("job_id", id))
		}
	}

	jws, err := s.fetchJobByID(ctx, id)
	if err != nil {
		if st, isStatus := status.FromError(err); isStatus {
			return nil, st.Err()
		}
		log.Error("failed to get job", zap.Error(err), zap.String("job_id", id))
		return nil, status.Error(codes.Internal, "failed to get job")
	}

	jobProto := toProto(&jws.Job, jws.Skills)
	if s.cache != nil {
		if encoded, err := encodeCachedJob(jobProto); err == nil {
			s.cache.Set(ctx, key, encoded, jobsCacheTTL)
		} else {
			log.Warn("failed to encode job for caching", zap.Error(err), zap.String("job_id", id))
		}
	}

	return &jobsv1.GetJobResponse{Job: jobProto}, nil
}

// requiredSkillsByJobID fetches every RequiredSkill row for the given job
// IDs in one query and groups the skill IDs by job_id.
func (s *Server) requiredSkillsByJobID(ctx context.Context, jobIDs []string) (map[string][]string, error) {
	out := make(map[string][]string, len(jobIDs))
	if len(jobIDs) == 0 {
		return out, nil
	}

	var rows []RequiredSkill
	if err := s.db.WithContext(ctx).Where("job_id IN ?", jobIDs).Order("skill_id").Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.JobID] = append(out[row.JobID], row.SkillID)
	}
	return out, nil
}

func jobIDs(rows []Job) []string {
	ids := make([]string, len(rows))
	for i := range rows {
		ids[i] = rows[i].ID
	}
	return ids
}

// dedupeNonEmpty trims and drops empty/duplicate skill IDs so a bad
// request can't create redundant or blank required-skill rows.
func dedupeNonEmpty(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func toProto(j *Job, requiredSkillIDs []string) *jobsv1.Job {
	return &jobsv1.Job{
		Id:               j.ID,
		Title:            j.Title,
		Description:      j.Description,
		RequiredSkillIds: requiredSkillIDs,
	}
}

// cachedJob is the JSON shape stored under jobs:<id> and (as a slice)
// jobs:all — a small DTO rather than the generated protobuf struct
// directly, same reasoning as skills-service's cachedSkill.
type cachedJob struct {
	ID               string   `json:"id"`
	Title            string   `json:"title"`
	Description      string   `json:"description"`
	RequiredSkillIDs []string `json:"required_skill_ids"`
}

func toCachedJob(j *jobsv1.Job) cachedJob {
	return cachedJob{ID: j.GetId(), Title: j.GetTitle(), Description: j.GetDescription(), RequiredSkillIDs: j.GetRequiredSkillIds()}
}

func (c cachedJob) toProto() *jobsv1.Job {
	return &jobsv1.Job{Id: c.ID, Title: c.Title, Description: c.Description, RequiredSkillIds: c.RequiredSkillIDs}
}

func encodeCachedJob(j *jobsv1.Job) (string, error) {
	b, err := json.Marshal(toCachedJob(j))
	return string(b), err
}

func decodeCachedJob(raw string) (*jobsv1.Job, error) {
	var c cachedJob
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return nil, err
	}
	return c.toProto(), nil
}

func encodeCachedJobs(rows []jobWithSkills) (string, error) {
	dtos := make([]cachedJob, len(rows))
	for i := range rows {
		dtos[i] = cachedJob{
			ID:               rows[i].Job.ID,
			Title:            rows[i].Job.Title,
			Description:      rows[i].Job.Description,
			RequiredSkillIDs: rows[i].Skills,
		}
	}
	b, err := json.Marshal(dtos)
	return string(b), err
}

func decodeCachedJobs(raw string) ([]*jobsv1.Job, error) {
	var dtos []cachedJob
	if err := json.Unmarshal([]byte(raw), &dtos); err != nil {
		return nil, err
	}
	out := make([]*jobsv1.Job, 0, len(dtos))
	for _, d := range dtos {
		out = append(out, d.toProto())
	}
	return out, nil
}
