package jobs

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	jobsv1 "github.com/Siddsharma25/skill-bridge-platform/backend/gen/jobs/v1"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/logger"
)

// Server implements jobsv1.JobsServiceServer. db may be nil — same
// degraded-start pattern as every other service in this codebase (see
// backend/CLAUDE.md).
type Server struct {
	jobsv1.UnimplementedJobsServiceServer

	db  *gorm.DB
	log *zap.Logger
}

// NewServer constructs a Server. log must not be nil; db may be nil.
func NewServer(db *gorm.DB, log *zap.Logger) *Server {
	return &Server{db: db, log: log}
}

// CreateJob is unauthenticated in Phase 1b — same reasoning as
// SkillsService.CreateSkill (see docs/DECISIONS.md). The job row and its
// required-skill rows are inserted in one transaction so a partial write
// (job created, skills half-inserted) can't happen.
func (s *Server) CreateJob(ctx context.Context, req *jobsv1.CreateJobRequest) (*jobsv1.CreateJobResponse, error) {
	log := logger.FromContext(ctx, s.log)

	title := strings.TrimSpace(req.GetTitle())
	if title == "" {
		return nil, status.Error(codes.InvalidArgument, "title is required")
	}
	if s.db == nil {
		return nil, status.Error(codes.Unavailable, "database is not configured on this instance")
	}

	job := Job{
		ID:          uuid.NewString(),
		Title:       title,
		Description: strings.TrimSpace(req.GetDescription()),
	}
	skillIDs := dedupeNonEmpty(req.GetRequiredSkillIds())

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&job).Error; err != nil {
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
	if err != nil {
		log.Error("failed to create job", zap.Error(err))
		return nil, status.Error(codes.Internal, "failed to create job")
	}

	log.Info("created job", zap.String("job_id", job.ID), zap.String("title", job.Title))
	return &jobsv1.CreateJobResponse{Job: toProto(&job, skillIDs)}, nil
}

// ListJobs returns every job posting, most-recently-created first, with
// required_skill_ids attached. Required skills are fetched in one
// additional query (grouped in memory by job_id) rather than per-job, so
// listing N jobs costs 2 queries total, not N+1 — a job's own
// required-skill IDs are jobs-service's own data, unlike the
// cross-service skill-name resolution the gateway leaves as a deliberate
// N+1 (see jobs.proto).
func (s *Server) ListJobs(ctx context.Context, _ *jobsv1.ListJobsRequest) (*jobsv1.ListJobsResponse, error) {
	log := logger.FromContext(ctx, s.log)

	if s.db == nil {
		return nil, status.Error(codes.Unavailable, "database is not configured on this instance")
	}

	var rows []Job
	if err := s.db.WithContext(ctx).Order("created_at DESC").Find(&rows).Error; err != nil {
		log.Error("failed to list jobs", zap.Error(err))
		return nil, status.Error(codes.Internal, "failed to list jobs")
	}

	skillsByJob, err := s.requiredSkillsByJobID(ctx, jobIDs(rows))
	if err != nil {
		log.Error("failed to list required skills", zap.Error(err))
		return nil, status.Error(codes.Internal, "failed to list jobs")
	}

	out := make([]*jobsv1.Job, 0, len(rows))
	for i := range rows {
		out = append(out, toProto(&rows[i], skillsByJob[rows[i].ID]))
	}
	return &jobsv1.ListJobsResponse{Jobs: out}, nil
}

// GetJob returns a single job posting by ID, or NotFound.
func (s *Server) GetJob(ctx context.Context, req *jobsv1.GetJobRequest) (*jobsv1.GetJobResponse, error) {
	log := logger.FromContext(ctx, s.log)

	id := strings.TrimSpace(req.GetId())
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	if s.db == nil {
		return nil, status.Error(codes.Unavailable, "database is not configured on this instance")
	}

	var job Job
	if err := s.db.WithContext(ctx).Where("id = ?", id).First(&job).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "job not found")
		}
		log.Error("failed to get job", zap.Error(err), zap.String("job_id", id))
		return nil, status.Error(codes.Internal, "failed to get job")
	}

	skillsByJob, err := s.requiredSkillsByJobID(ctx, []string{job.ID})
	if err != nil {
		log.Error("failed to get required skills", zap.Error(err), zap.String("job_id", id))
		return nil, status.Error(codes.Internal, "failed to get job")
	}

	return &jobsv1.GetJobResponse{Job: toProto(&job, skillsByJob[job.ID])}, nil
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
