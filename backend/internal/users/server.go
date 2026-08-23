package users

import (
	"context"
	"errors"
	"strings"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	usersv1 "github.com/Siddsharma25/skill-bridge-platform/backend/gen/users/v1"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/logger"
)

// Server implements usersv1.UsersServiceServer. db may be nil — same
// degraded-start pattern as every other service in this codebase (see
// backend/CLAUDE.md).
type Server struct {
	usersv1.UnimplementedUsersServiceServer

	db  *gorm.DB
	log *zap.Logger
}

// NewServer constructs a Server. log must not be nil; db may be nil.
func NewServer(db *gorm.DB, log *zap.Logger) *Server {
	return &Server{db: db, log: log}
}

// GetProfile returns userID's profile, lazily creating an empty one on
// first touch. See docs/DECISIONS.md for why this is a Phase 1b stand-in
// for a Kafka-consumed provisioning flow rather than an oversight.
func (s *Server) GetProfile(ctx context.Context, req *usersv1.GetProfileRequest) (*usersv1.GetProfileResponse, error) {
	log := logger.FromContext(ctx, s.log)

	userID := strings.TrimSpace(req.GetUserId())
	if userID == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if s.db == nil {
		return nil, status.Error(codes.Unavailable, "database is not configured on this instance")
	}

	profile, err := s.getOrCreateProfile(ctx, userID)
	if err != nil {
		log.Error("failed to get or create profile", zap.Error(err), zap.String("user_id", userID))
		return nil, status.Error(codes.Internal, "failed to load profile")
	}
	return &usersv1.GetProfileResponse{Profile: toProfileProto(profile)}, nil
}

// UpdateProfile lazily creates the profile on first touch (same as
// GetProfile), then applies whichever of display_name/bio the caller
// actually supplied — both are optional in the proto so an unset field
// never clobbers the existing value with an empty string.
func (s *Server) UpdateProfile(ctx context.Context, req *usersv1.UpdateProfileRequest) (*usersv1.UpdateProfileResponse, error) {
	log := logger.FromContext(ctx, s.log)

	userID := strings.TrimSpace(req.GetUserId())
	if userID == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if s.db == nil {
		return nil, status.Error(codes.Unavailable, "database is not configured on this instance")
	}

	profile, err := s.getOrCreateProfile(ctx, userID)
	if err != nil {
		log.Error("failed to get or create profile", zap.Error(err), zap.String("user_id", userID))
		return nil, status.Error(codes.Internal, "failed to load profile")
	}

	updates := map[string]any{}
	if req.DisplayName != nil {
		profile.DisplayName = req.GetDisplayName()
		updates["display_name"] = profile.DisplayName
	}
	if req.Bio != nil {
		profile.Bio = req.GetBio()
		updates["bio"] = profile.Bio
	}
	if len(updates) > 0 {
		if err := s.db.WithContext(ctx).Model(&Profile{}).Where("user_id = ?", userID).Updates(updates).Error; err != nil {
			log.Error("failed to update profile", zap.Error(err), zap.String("user_id", userID))
			return nil, status.Error(codes.Internal, "failed to update profile")
		}
	}

	log.Info("updated profile", zap.String("user_id", userID))
	return &usersv1.UpdateProfileResponse{Profile: toProfileProto(profile)}, nil
}

// AddUserSkill upserts a (user_id, skill_id) -> proficiency row. skill_id
// is trusted as a valid skills-service ID; users-service cannot verify it
// against skills-service's schema (schema-per-service isolation) — see
// users.proto.
func (s *Server) AddUserSkill(ctx context.Context, req *usersv1.AddUserSkillRequest) (*usersv1.AddUserSkillResponse, error) {
	log := logger.FromContext(ctx, s.log)

	userID := strings.TrimSpace(req.GetUserId())
	skillID := strings.TrimSpace(req.GetSkillId())
	proficiency := strings.TrimSpace(req.GetProficiency())
	if userID == "" || skillID == "" || proficiency == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id, skill_id, and proficiency are required")
	}
	if s.db == nil {
		return nil, status.Error(codes.Unavailable, "database is not configured on this instance")
	}

	row := UserSkill{UserID: userID, SkillID: skillID, Proficiency: proficiency}
	err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}, {Name: "skill_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"proficiency", "updated_at"}),
	}).Create(&row).Error
	if err != nil {
		log.Error("failed to upsert user skill", zap.Error(err), zap.String("user_id", userID), zap.String("skill_id", skillID))
		return nil, status.Error(codes.Internal, "failed to add skill")
	}

	log.Info("added user skill", zap.String("user_id", userID), zap.String("skill_id", skillID))
	return &usersv1.AddUserSkillResponse{}, nil
}

// ListUserSkills returns every (skill_id, proficiency) pair for userID. It
// deliberately does not resolve skill names/categories — that's
// skills-service's data, joined in by the gateway (a small, deliberate N+1
// left for Phase 1c's dataloader work — see users.proto).
func (s *Server) ListUserSkills(ctx context.Context, req *usersv1.ListUserSkillsRequest) (*usersv1.ListUserSkillsResponse, error) {
	log := logger.FromContext(ctx, s.log)

	userID := strings.TrimSpace(req.GetUserId())
	if userID == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if s.db == nil {
		return nil, status.Error(codes.Unavailable, "database is not configured on this instance")
	}

	var rows []UserSkill
	if err := s.db.WithContext(ctx).Where("user_id = ?", userID).Order("skill_id").Find(&rows).Error; err != nil {
		log.Error("failed to list user skills", zap.Error(err), zap.String("user_id", userID))
		return nil, status.Error(codes.Internal, "failed to list skills")
	}

	out := make([]*usersv1.UserSkill, 0, len(rows))
	for i := range rows {
		out = append(out, &usersv1.UserSkill{SkillId: rows[i].SkillID, Proficiency: rows[i].Proficiency})
	}
	return &usersv1.ListUserSkillsResponse{UserSkills: out}, nil
}

// getOrCreateProfile fetches userID's profile, creating an empty one if
// none exists yet. The insert uses ON CONFLICT DO NOTHING followed by a
// re-fetch rather than a plain check-then-insert, so two concurrent
// first-touch requests for the same user_id can't race into a duplicate
// key error — one wins the insert, the other's DO NOTHING is a no-op, and
// both re-fetch the same canonical row.
func (s *Server) getOrCreateProfile(ctx context.Context, userID string) (*Profile, error) {
	var profile Profile
	err := s.db.WithContext(ctx).Where("user_id = ?", userID).First(&profile).Error
	if err == nil {
		return &profile, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	fresh := Profile{UserID: userID}
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoNothing: true,
	}).Create(&fresh).Error; err != nil {
		return nil, err
	}

	if err := s.db.WithContext(ctx).Where("user_id = ?", userID).First(&profile).Error; err != nil {
		return nil, err
	}
	return &profile, nil
}

func toProfileProto(p *Profile) *usersv1.Profile {
	return &usersv1.Profile{
		UserId:      p.UserID,
		DisplayName: p.DisplayName,
		Bio:         p.Bio,
	}
}
