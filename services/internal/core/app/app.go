// Package app wires the core service (port 8080): configuration from the environment, dependencies, the HTTP
// middleware chain and every route of contracts/openapi.yaml, plus the background AI-cron and the Kafka outbox
// publisher. Core starts and answers /healthz even when the agent or the MCP servers are down; chat then streams
// error{upstream_unavailable} (R79).
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"aibuddy/services/internal/core/actions"
	"aibuddy/services/internal/core/agentclient"
	"aibuddy/services/internal/core/analytics"
	"aibuddy/services/internal/core/approvals"
	"aibuddy/services/internal/core/authapi"
	"aibuddy/services/internal/core/chat"
	"aibuddy/services/internal/core/cron"
	"aibuddy/services/internal/core/escalations"
	"aibuddy/services/internal/core/flags"
	"aibuddy/services/internal/core/mcpclient"
	"aibuddy/services/internal/core/notify"
	"aibuddy/services/internal/core/plan"
	"aibuddy/services/internal/core/ratelimit"
	"aibuddy/services/internal/platform/authn"
	"aibuddy/services/internal/platform/config"
	"aibuddy/services/internal/platform/httpx"
	"aibuddy/services/internal/platform/pg"
)

// Config holds core settings (docs/backend/IMPLEMENTATION.md §7, .env.example).
type Config struct {
	Port               int
	PostgresURL        string
	RedisURL           string // empty: no Redis (no rate limiting, flags read from the DB)
	JWTSecret          string
	ConfirmationSecret string
	AgentURL           string
	AgentServiceToken  string // optional X-Service-Token for the agent
	MCPBaseURL         string
	KafkaBrokers       []string // empty: Kafka off
	CORSOrigins        []string
	LLMTimeout         time.Duration
	CronInterval       time.Duration
	ChatRatePerMin     int
	JWTTTL             time.Duration

	envErr error // invalid values found by LoadConfig
}

// LoadConfig reads configuration from getenv (os.Getenv in production); Validate reports problems.
func LoadConfig(getenv func(string) string) Config {
	env := config.New(getenv)
	cfg := Config{
		Port:               env.Int("CORE_PORT", 8080),
		PostgresURL:        env.String("POSTGRES_URL", "postgres://buddy:buddy@postgres:5432/buddy?sslmode=disable"),
		RedisURL:           env.String("REDIS_URL", "redis://redis:6379/0"),
		JWTSecret:          env.String("JWT_SECRET", ""),
		ConfirmationSecret: env.String("CONFIRMATION_HMAC_SECRET", ""),
		AgentURL:           env.String("AGENT_URL", "http://agent:8090"),
		AgentServiceToken:  env.String("AGENT_SERVICE_TOKEN", ""),
		MCPBaseURL:         env.String("MCP_BASE_URL", mcpclient.DefaultBaseURL),
		KafkaBrokers:       env.List("KAFKA_BROKERS", nil),
		CORSOrigins:        env.List("CORS_ORIGINS", []string{"http://localhost:5173"}),
		LLMTimeout:         env.Duration("LLM_TIMEOUT_S", 30*time.Second),
		CronInterval:       env.Duration("CRON_INTERVAL", 24*time.Hour),
		ChatRatePerMin:     env.Int("CHAT_RATE_PER_MIN", chat.DefaultRatePerMin),
		JWTTTL:             env.Duration("JWT_TTL", 12*time.Hour),
	}
	cfg.envErr = env.Err()
	return cfg
}

// Validate fails on invalid values and missing secrets: the service must not start with empty secrets.
func (c Config) Validate() error {
	var errs []error
	if c.envErr != nil {
		errs = append(errs, c.envErr)
	}
	for _, s := range [][2]string{{"JWT_SECRET", c.JWTSecret}, {"CONFIRMATION_HMAC_SECRET", c.ConfirmationSecret}} {
		if s[1] == "" {
			errs = append(errs, fmt.Errorf("missing required environment variable %s", s[0]))
		}
	}
	if c.Port <= 0 || c.Port > 65535 {
		errs = append(errs, fmt.Errorf("CORE_PORT %d is out of range", c.Port))
	}
	return errors.Join(errs...)
}

// App is the wired core service.
type App struct {
	Handler http.Handler
	Cron    *cron.Runner
	Emitter *analytics.Emitter
}

// New wires every package of core over pool and rdb (nil: no Redis). It does not contact the agent or MCP.
func New(cfg Config, pool *pgxpool.Pool, rdb redis.Cmdable, log *slog.Logger) (*App, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	jwt, hmac := []byte(cfg.JWTSecret), []byte(cfg.ConfirmationSecret)
	mcp, err := mcpclient.New(mcpclient.Config{BaseURL: cfg.MCPBaseURL, JWTSecret: jwt, ConfirmationSecret: hmac})
	if err != nil {
		return nil, err
	}
	agent := agentclient.New(cfg.AgentURL, cfg.LLMTimeout)
	agent.ServiceToken = cfg.AgentServiceToken

	emitter := analytics.NewEmitter(len(cfg.KafkaBrokers) > 0)
	flagSvc := flags.NewService(pool, rdb, emitter, log)
	acts := actions.NewService(emitter, notify.Store{}, log)
	runner := cron.NewRunner(pool, emitter, cfg.CronInterval, log)
	auth := authapi.New(pool, jwt, cfg.JWTTTL, flagSvc, emitter, log)

	api := http.NewServeMux()
	auth.Register(api)
	plan.New(pool, emitter, log).Register(api)
	chat.New(chat.Deps{Pool: pool, Agent: agent, Actions: acts, Limiter: ratelimit.New(rdb), Flags: flagSvc, Emitter: emitter, Log: log},
		chat.Config{ConfirmationSecret: hmac, RatePerMin: cfg.ChatRatePerMin}).Register(api)
	actions.New(pool, acts, log).Register(api)
	approvals.New(pool, mcp, acts, emitter, log).Register(api)
	escalations.New(pool, emitter, flagSvc, mcp, log).Register(api)
	notify.New(pool, log).Register(api)
	analytics.New(pool, emitter, log).Register(api)
	flags.New(flagSvc, log).Register(api)
	cron.New(runner, log).Register(api)

	root := http.NewServeMux()
	root.HandleFunc("GET /healthz", healthz(pool))
	auth.RegisterPublic(root) // POST /api/auth/login: the only /api route without a JWT
	root.Handle("/api/", authn.Middleware(jwt)(api))
	h := httpx.Chain(httpx.RequestID, httpx.Recover(log), httpx.AccessLog(log), httpx.CORS(cfg.CORSOrigins))(root)
	return &App{Handler: h, Cron: runner, Emitter: emitter}, nil
}

// healthz answers 200 when Postgres answers a ping, 503 otherwise (the agent is not checked: core must stay up).
func healthz(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := pool.Ping(ctx); err != nil {
			httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "degraded", "service": "core"})
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "core"})
	}
}

// Run connects to Postgres (with retry while it starts) and Redis, starts the AI-cron and, with KAFKA_BROKERS, the
// outbox publisher, and serves until ctx is cancelled.
func Run(ctx context.Context, cfg Config, log *slog.Logger) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if log == nil {
		log = slog.Default()
	}
	pool, err := pg.Open(ctx, cfg.PostgresURL, pg.Options{})
	if err != nil {
		return err
	}
	defer pool.Close()
	var rdb redis.Cmdable // stays a nil interface without Redis
	if cfg.RedisURL != "" {
		opts, err := redis.ParseURL(cfg.RedisURL)
		if err != nil {
			return fmt.Errorf("REDIS_URL: %w", err)
		}
		client := redis.NewClient(opts)
		defer client.Close()
		rdb = client
	}
	a, err := New(cfg, pool, rdb, log)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go a.Cron.Run(ctx)
	if len(cfg.KafkaBrokers) > 0 {
		prod, err := analytics.NewKafkaProducer(cfg.KafkaBrokers)
		if err != nil {
			return err
		}
		defer prod.Close()
		go analytics.NewPublisher(pool, prod, log).Run(ctx)
		log.Info("kafka publisher started", "brokers", cfg.KafkaBrokers)
	}
	return serve(ctx, fmt.Sprintf(":%d", cfg.Port), a.Handler, log)
}

func serve(ctx context.Context, addr string, h http.Handler, log *slog.Logger) error {
	srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 5 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("listening", "addr", addr)
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// Healthcheck performs GET url and fails on a non-200 answer (Docker HEALTHCHECK via `-healthcheck`).
func Healthcheck(url string) error {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck: status %d", resp.StatusCode)
	}
	return nil
}
