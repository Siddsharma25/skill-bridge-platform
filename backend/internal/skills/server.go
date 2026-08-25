package skills

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	skillsv1 "github.com/Siddsharma25/skill-bridge-platform/backend/gen/skills/v1"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/cache"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/logger"
)

// skillsAllCacheKey is the single write-through cache entry for the whole
// taxonomy (see ListSkills/CreateSkill). skillsCacheTTL is deliberately
// long — the taxonomy changes rarely, and every mutation (CreateSkill)
// explicitly DELs this key in the same write path anyway (write-through,
// per docs/DECISIONS.md — there's no Kafka yet to invalidate it
// event-driven, and even once there is, a service invalidating its own
// cache on its own write doesn't need an event round trip).
const (
	skillsAllCacheKey = "skills:all"
	skillsCacheTTL    = 1 * time.Hour
)

// Server implements skillsv1.SkillsServiceServer. db may be nil — same
// degraded-start pattern as auth-service (see docs/DECISIONS.md and
// backend/CLAUDE.md): the service starts and serves health checks even
// without a reachable database, and every RPC that needs one returns a
// clear Unavailable status instead of the process crashing at startup.
// cache may also be nil/disabled (see internal/platform/cache) — caching
// is purely an optimization, never a correctness dependency.
type Server struct {
	skillsv1.UnimplementedSkillsServiceServer

	db    *gorm.DB
	cache cache.Cache
	log   *zap.Logger

	// fetchAllSkills and insertSkill default to thin wrappers over s.db
	// (queryAllSkillsFromDB / insertSkillIntoDB, below) but are swappable
	// fields rather than plain method calls specifically so
	// server_test.go can inject a call-counting fake and prove a cache hit
	// short-circuits the database path, without standing up a real
	// database for that proof. Every other read/write in this codebase
	// still goes straight through s.db (see backend/CLAUDE.md) — this
	// seam exists only because Phase 1c's cache tests need to observe
	// "was the source of truth actually hit," which a bare *gorm.DB field
	// doesn't allow to verify without a live database. See
	// docs/DECISIONS.md.
	fetchAllSkills func(ctx context.Context) ([]Skill, error)
	insertSkill    func(ctx context.Context, skill *Skill) error
}

// NewServer constructs a Server. log must not be nil; db and c may both be
// nil (a nil c behaves like a disabled cache.Cache, same as
// cache.NewFromEnv would return for an unset REDIS_URL).
func NewServer(db *gorm.DB, c cache.Cache, log *zap.Logger) *Server {
	s := &Server{db: db, cache: c, log: log}
	s.fetchAllSkills = s.queryAllSkillsFromDB
	s.insertSkill = s.insertSkillIntoDB
	return s
}

func (s *Server) queryAllSkillsFromDB(ctx context.Context) ([]Skill, error) {
	if s.db == nil {
		return nil, status.Error(codes.Unavailable, "database is not configured on this instance")
	}
	var rows []Skill
	if err := s.db.WithContext(ctx).Order("name").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *Server) insertSkillIntoDB(ctx context.Context, skill *Skill) error {
	if s.db == nil {
		return status.Error(codes.Unavailable, "database is not configured on this instance")
	}
	return s.db.WithContext(ctx).Create(skill).Error
}

// CreateSkill is unauthenticated in Phase 1b — there is no role system yet
// (see docs/DECISIONS.md), so anyone can add a skill to the taxonomy.
// Successfully creating a skill DELs skills:all in the same write path
// (write-through cache invalidation, per docs/DECISIONS.md) so ListSkills
// never serves a stale taxonomy after this returns.
func (s *Server) CreateSkill(ctx context.Context, req *skillsv1.CreateSkillRequest) (*skillsv1.CreateSkillResponse, error) {
	log := logger.FromContext(ctx, s.log)

	name := strings.TrimSpace(req.GetName())
	category := strings.TrimSpace(req.GetCategory())
	if name == "" || category == "" {
		return nil, status.Error(codes.InvalidArgument, "name and category are required")
	}

	skill := Skill{
		ID:       uuid.NewString(),
		Name:     name,
		Category: category,
	}
	if err := s.insertSkill(ctx, &skill); err != nil {
		if _, isStatus := status.FromError(err); isStatus {
			return nil, err
		}
		if isDuplicateKey(err) {
			return nil, status.Error(codes.AlreadyExists, "a skill with this name already exists")
		}
		log.Error("failed to insert skill", zap.Error(err))
		return nil, status.Error(codes.Internal, "failed to create skill")
	}

	if s.cache != nil {
		s.cache.Del(ctx, skillsAllCacheKey)
	}

	log.Info("created skill", zap.String("skill_id", skill.ID), zap.String("name", skill.Name))
	return &skillsv1.CreateSkillResponse{Skill: toProto(&skill)}, nil
}

// ListSkills returns every skill in the taxonomy, ordered by name. No
// pagination yet — fine at this data scale (see skills.proto). Served from
// the skills:all cache entry when present; on a miss, falls through to the
// database and populates the cache for next time.
func (s *Server) ListSkills(ctx context.Context, _ *skillsv1.ListSkillsRequest) (*skillsv1.ListSkillsResponse, error) {
	log := logger.FromContext(ctx, s.log)

	if s.cache != nil {
		if cached, ok := s.cache.Get(ctx, skillsAllCacheKey); ok {
			skills, err := decodeCachedSkills(cached)
			if err == nil {
				return &skillsv1.ListSkillsResponse{Skills: skills}, nil
			}
			log.Warn("failed to decode cached skills; falling through to database", zap.Error(err))
		}
	}

	rows, err := s.fetchAllSkills(ctx)
	if err != nil {
		if _, isStatus := status.FromError(err); isStatus {
			return nil, err
		}
		log.Error("failed to list skills", zap.Error(err))
		return nil, status.Error(codes.Internal, "failed to list skills")
	}

	out := make([]*skillsv1.Skill, 0, len(rows))
	for i := range rows {
		out = append(out, toProto(&rows[i]))
	}

	if s.cache != nil {
		if encoded, err := encodeCachedSkills(rows); err == nil {
			s.cache.Set(ctx, skillsAllCacheKey, encoded, skillsCacheTTL)
		} else {
			log.Warn("failed to encode skills for caching", zap.Error(err))
		}
	}

	return &skillsv1.ListSkillsResponse{Skills: out}, nil
}

// GetSkillsByIds returns every skill matching one of ids (any id with no
// match is silently omitted). Added in Phase 1c specifically so the
// gateway's dataloader (internal/gateway/graph/dataloader) can batch every
// distinct skill_id referenced across a whole GraphQL response into one
// call, replacing the Phase 1b pattern of the gateway fetching the entire
// taxonomy via ListSkills once per parent object — see skills.proto and
// docs/DECISIONS.md. Not cached (unlike ListSkills) — it's already a
// batched, targeted lookup rather than the whole-taxonomy scan ListSkills
// does, so the same write-through cache entry doesn't apply cleanly, and
// adding a second cache shape purely for this wasn't judged worth it at
// this data scale.
//
//nolint:revive // must match the generated skillsv1.SkillsServiceServer interface method name (skills.proto's rpc is literally named GetSkillsByIds)
func (s *Server) GetSkillsByIds(ctx context.Context, req *skillsv1.GetSkillsByIdsRequest) (*skillsv1.GetSkillsByIdsResponse, error) {
	log := logger.FromContext(ctx, s.log)

	ids := dedupeNonEmpty(req.GetIds())
	if len(ids) == 0 {
		return &skillsv1.GetSkillsByIdsResponse{}, nil
	}
	if s.db == nil {
		return nil, status.Error(codes.Unavailable, "database is not configured on this instance")
	}

	var rows []Skill
	if err := s.db.WithContext(ctx).Where("id IN ?", ids).Find(&rows).Error; err != nil {
		log.Error("failed to get skills by ids", zap.Error(err))
		return nil, status.Error(codes.Internal, "failed to get skills")
	}

	out := make([]*skillsv1.Skill, 0, len(rows))
	for i := range rows {
		out = append(out, toProto(&rows[i]))
	}
	return &skillsv1.GetSkillsByIdsResponse{Skills: out}, nil
}

// dedupeNonEmpty trims and drops empty/duplicate IDs — same helper jobs.go
// already has for required-skill IDs; kept as a separate copy here rather
// than a shared platform package for two call sites in different services
// with no other reason to depend on each other.
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

// cachedSkill is the JSON shape stored under skillsAllCacheKey — a small
// DTO rather than the generated protobuf struct directly, so caching
// doesn't depend on protobuf-go's internal field layout staying
// json.Marshal-friendly.
type cachedSkill struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
}

func encodeCachedSkills(rows []Skill) (string, error) {
	dtos := make([]cachedSkill, len(rows))
	for i, r := range rows {
		dtos[i] = cachedSkill{ID: r.ID, Name: r.Name, Category: r.Category}
	}
	b, err := json.Marshal(dtos)
	return string(b), err
}

func decodeCachedSkills(raw string) ([]*skillsv1.Skill, error) {
	var dtos []cachedSkill
	if err := json.Unmarshal([]byte(raw), &dtos); err != nil {
		return nil, err
	}
	out := make([]*skillsv1.Skill, 0, len(dtos))
	for _, d := range dtos {
		out = append(out, &skillsv1.Skill{Id: d.ID, Name: d.Name, Category: d.Category})
	}
	return out, nil
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
