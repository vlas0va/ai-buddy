// Пакет flags отдаёт runtime feature flags (R55) из core.feature_flags с кешем в Redis (TTL 30 с, при сбое — из базы) и
// таргетингом по командам. GET /api/flags открыт всем пользователям; PUT /api/flags/{key} есть только в демо-сборках
// (buildflags.Demo) и только для роли руководителя.
package flags

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/jackc/pgx/v5/pgxpool"

	"aibuddy/services/internal/core/analytics"
	"aibuddy/services/internal/platform/apierr"
	"aibuddy/services/internal/platform/authn"
	"aibuddy/services/internal/platform/buildflags"
	"aibuddy/services/internal/platform/httpx"
)

// Ключи флагов (openapi FlagKey).
const (
	ScenarioS1      = "scenario_s1"
	ScenarioS2      = "scenario_s2"
	ScenarioS3      = "scenario_s3"
	ScenarioS4      = "scenario_s4"
	ProactiveDigest = "proactive_digest"
	EscalationToKB  = "escalation_to_kb"
)

var knownKeys = []string{ScenarioS1, ScenarioS2, ScenarioS3, ScenarioS4, ProactiveDigest, EscalationToKB}

// CacheTTL — сколько таблица флагов хранится в кеше Redis.
const CacheTTL = 30 * time.Second

const cacheKey = "flags:v1"

// Flag — одна строка core.feature_flags. Пустой Teams означает все команды.
type Flag struct {
	Key     string   `json:"key"`
	Enabled bool     `json:"enabled"`
	Teams   []string `json:"teams"`
}

// On сообщает, включён ли флаг для команды team.
func (f Flag) On(team string) bool {
	return f.Enabled && (len(f.Teams) == 0 || slices.Contains(f.Teams, team))
}

// Service читает и пишет флаги.
type Service struct {
	pool    *pgxpool.Pool
	rdb     redis.Cmdable
	emitter *analytics.Emitter
	log     *slog.Logger
}

// NewService возвращает сервис флагов. rdb может быть nil (без кеша); это не должен быть типизированный nil-указатель.
func NewService(pool *pgxpool.Pool, rdb redis.Cmdable, emitter *analytics.Emitter, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{pool: pool, rdb: rdb, emitter: emitter, log: log}
}

// All возвращает все флаги — из кеша Redis, если он доступен и свеж, иначе из базы.
func (s *Service) All(ctx context.Context) ([]Flag, error) {
	if s.rdb != nil {
		raw, err := s.rdb.Get(ctx, cacheKey).Bytes()
		if err == nil {
			var fl []Flag
			if json.Unmarshal(raw, &fl) == nil {
				return fl, nil
			}
		} else if err != redis.Nil {
			s.log.Warn("flags cache read failed, using database", "err", err)
		}
	}
	fl, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	if s.rdb != nil {
		if raw, err := json.Marshal(fl); err == nil {
			if err := s.rdb.Set(ctx, cacheKey, raw, CacheTTL).Err(); err != nil {
				s.log.Warn("flags cache write failed", "err", err)
			}
		}
	}
	return fl, nil
}

func (s *Service) load(ctx context.Context) ([]Flag, error) {
	rows, err := s.pool.Query(ctx, `SELECT key, enabled, teams FROM core.feature_flags ORDER BY key`)
	if err != nil {
		return nil, fmt.Errorf("select flags: %w", err)
	}
	defer rows.Close()
	out := []Flag{}
	for rows.Next() {
		var f Flag
		if err := rows.Scan(&f.Key, &f.Enabled, &f.Teams); err != nil {
			return nil, fmt.Errorf("scan flag: %w", err)
		}
		if f.Teams == nil {
			f.Teams = []string{}
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read flags: %w", err)
	}
	return out, nil
}

// ForTeam возвращает key -> enabled для команды team (с учётом таргетинга).
func (s *Service) ForTeam(ctx context.Context, team string) (map[string]bool, error) {
	fl, err := s.All(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(fl))
	for _, f := range fl {
		out[f.Key] = f.On(team)
	}
	return out, nil
}

// Enabled сообщает, включён ли key для команды team; неизвестные ключи и ошибки чтения считаются выключенными.
func (s *Service) Enabled(ctx context.Context, key, team string) bool {
	fl, err := s.All(ctx)
	if err != nil {
		s.log.Error("flag lookup failed", "key", key, "err", err)
		return false
	}
	for _, f := range fl {
		if f.Key == key {
			return f.On(team)
		}
	}
	return false
}

// Set вставляет или обновляет флаг, отправляет flag.changed и сбрасывает кеш. Пустой teams — все команды.
func (s *Service) Set(ctx context.Context, actor authn.Claims, key string, enabled bool, teams []string) error {
	if teams == nil {
		teams = []string{}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `INSERT INTO core.feature_flags (key, enabled, teams) VALUES ($1, $2, $3)
		ON CONFLICT (key) DO UPDATE SET enabled = EXCLUDED.enabled, teams = EXCLUDED.teams, updated_at = now()`,
		key, enabled, teams); err != nil {
		return fmt.Errorf("upsert flag: %w", err)
	}
	if err := s.emitter.Emit(ctx, tx, analytics.Event{Name: "flag.changed", UserID: actor.Sub, Role: actor.Role, TeamID: actor.TeamID,
		Props: map[string]any{"key": key, "enabled": enabled, "teams_count": len(teams)}}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	if s.rdb != nil {
		if err := s.rdb.Del(ctx, cacheKey).Err(); err != nil {
			s.log.Warn("flags cache invalidation failed; stale for up to 30s", "err", err)
		}
	}
	return nil
}

// Handler обслуживает /api/flags.
type Handler struct {
	svc *Service
	log *slog.Logger
	// Demo включает PUT /api/flags/{key}; по умолчанию buildflags.Demo, это поле, чтобы тесты могли его переключать.
	Demo bool
}

// New возвращает HTTP-обработчик флагов.
func New(svc *Service, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{svc: svc, log: log, Demo: buildflags.Demo}
}

// Register добавляет GET /api/flags и PUT /api/flags/{key} (нужна авторизация; вне демо-сборок PUT отвечает 404).
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/flags", h.get)
	mux.HandleFunc("PUT /api/flags/{key}", h.put)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	c, ok := authn.FromContext(r.Context())
	if !ok {
		apierr.WriteJSON(w, apierr.Unauthorized())
		return
	}
	m, err := h.svc.ForTeam(r.Context(), c.TeamID)
	if err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, m)
}

var teamRE = regexp.MustCompile(`^[a-z0-9_-]{1,40}$`)

func (h *Handler) put(w http.ResponseWriter, r *http.Request) {
	if !h.Demo {
		apierr.WriteJSON(w, apierr.NotFound())
		return
	}
	c, ok := authn.FromContext(r.Context())
	if !ok {
		apierr.WriteJSON(w, apierr.Unauthorized())
		return
	}
	if c.Role != "lead" {
		apierr.WriteJSON(w, apierr.Forbidden())
		return
	}
	key := r.PathValue("key")
	if !slices.Contains(knownKeys, key) {
		apierr.WriteJSON(w, apierr.Validation().WithMessage("Неизвестный флаг"))
		return
	}
	var body struct {
		Enabled *bool    `json:"enabled"`
		Teams   []string `json:"teams"`
	}
	if err := httpx.ReadJSON(w, r, &body); err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	if body.Enabled == nil || len(body.Teams) > 20 {
		apierr.WriteJSON(w, apierr.Validation().WithMessage("Укажите enabled и не более 20 команд"))
		return
	}
	for _, t := range body.Teams {
		if !teamRE.MatchString(t) {
			apierr.WriteJSON(w, apierr.Validation().WithMessage("Недопустимый идентификатор команды"))
			return
		}
	}
	if err := h.svc.Set(r.Context(), c, key, *body.Enabled, body.Teams); err != nil {
		apierr.WriteJSON(w, err)
		return
	}
	h.get(w, r)
}
