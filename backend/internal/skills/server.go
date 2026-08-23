package skills

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	skillsv1 "github.com/Siddsharma25/skill-bridge-platform/backend/gen/skills/v1"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/logger"
)

// Server implements skillsv1.SkillsServiceServer. db may be nil — same
// degraded-start pattern as auth-service (see docs/DECISIONS.md and
// backend/CLAUDE.md): the service starts and serves health checks even
// without a reachable database, and every RPC that needs one returns a
// clear Unavailable status instead of the process crashing at startup.
type Server struct {
	skillsv1.UnimplementedSkillsServiceServer

	db  *gorm.DB
	log *zap.Logger
}

// NewServer constructs a Server. log must not be nil; db may be nil.
func NewServer(db *gorm.DB, log *zap.Logger) *Server {
	return &Server{db: db, log: log}
}

// CreateSkill is unauthenticated in Phase 1b — there is no role system yet
// (see docs/DECISIONS.md), so anyone can add a skill to the taxonomy.
func (s *Server) CreateSkill(ctx context.Context, req *skillsv1.CreateSkillRequest) (*skillsv1.CreateSkillResponse, error) {
	log := logger.FromContext(ctx, s.log)

	name := strings.TrimSpace(req.GetName())
	category := strings.TrimSpace(req.GetCategory())
	if name == "" || category == "" {
		return nil, status.Error(codes.InvalidArgument, "name and category are required")
	}
	if s.db == nil {
		return nil, status.Error(codes.Unavailable, "database is not configured on this instance")
	}

	skill := Skill{
		ID:       uuid.NewString(),
		Name:     name,
		Category: category,
	}
	if err := s.db.WithContext(ctx).Create(&skill).Error; err != nil {
		if isDuplicateKey(err) {
			return nil, status.Error(codes.AlreadyExists, "a skill with this name already exists")
		}
		log.Error("failed to insert skill", zap.Error(err))
		return nil, status.Error(codes.Internal, "failed to create skill")
	}

	log.Info("created skill", zap.String("skill_id", skill.ID), zap.String("name", skill.Name))
	return &skillsv1.CreateSkillResponse{Skill: toProto(&skill)}, nil
}

// ListSkills returns every skill in the taxonomy, ordered by name. No
// pagination yet — fine at this data scale (see skills.proto).
func (s *Server) ListSkills(ctx context.Context, _ *skillsv1.ListSkillsRequest) (*skillsv1.ListSkillsResponse, error) {
	log := logger.FromContext(ctx, s.log)

	if s.db == nil {
		return nil, status.Error(codes.Unavailable, "database is not configured on this instance")
	}

	var rows []Skill
	if err := s.db.WithContext(ctx).Order("name").Find(&rows).Error; err != nil {
		log.Error("failed to list skills", zap.Error(err))
		return nil, status.Error(codes.Internal, "failed to list skills")
	}

	out := make([]*skillsv1.Skill, 0, len(rows))
	for i := range rows {
		out = append(out, toProto(&rows[i]))
	}
	return &skillsv1.ListSkillsResponse{Skills: out}, nil
}

func toProto(s *Skill) *skillsv1.Skill {
	return &skillsv1.Skill{
		Id:       s.ID,
		Name:     s.Name,
		Category: s.Category,
	}
}

// isDuplicateKey reports whether err is a Postgres unique-violation
// (SQLSTATE 23505) — same check as auth-service's, see
// internal/auth/server.go.
func isDuplicateKey(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}
