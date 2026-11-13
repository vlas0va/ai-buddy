// Пакет app собирает сервис ядра (порт 8080): конфигурацию из окружения, зависимости, цепочку HTTP-middleware и все
// маршруты contracts/openapi.yaml, плюс фоновый AI-cron и публикатор outbox в Kafka. Ядро стартует и отвечает на
// /healthz, даже когда агент или MCP-серверы недоступны; чат тогда отдаёт в потоке error{upstream_unavailable} (R79).
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

// Config хранит настройки ядра (docs/backend/IMPLEMENTATION.md §7, .env.example).
type Config struct {
	Port               int
	PostgresURL        string
	RedisURL           string // пусто: без Redis (без ограничения частоты, флаги читаются из БД)
	JWTSecret          string
	ConfirmationSecret string
	AgentURL           string
	AgentServiceToken  string // необязательный X-Service-Token для агента
	MCPBaseURL         string
	KafkaBrokers       []string // пусто: Kafka выключена
	CORSOrigins        []string
	LLMTimeout         time.Duration
	CronInterval       time.Duration
	ChatRatePerMin     int
	JWTTTL             time.Duration

	envErr error // некорректные значения, найденные LoadConfig
}

// LoadConfig читает конфигурацию из getenv (os.Getenv в продакшене); о проблемах сообщает Validate.
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

// Validate падает на некорректных значениях и отсутствующих секретах: сервис не должен стартовать с пустыми секретами.
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

// App — собранный сервис ядра.
type App struct {
	Handler http.Handler
	Cron    *cron.Runner
	Emitter *analytics.Emitter
}

// New собирает все пакеты ядра поверх pool и rdb (nil: без Redis). К агенту и MCP не обращается.
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
	chat.New(chat.Deps{Pool: pool, Agent: agent, Actions: acts, Limiter: ratelimit.New(rdb), Flags: flagSvc, Emitter: emitter, MCP: mcp, Log: log},
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
	auth.RegisterPublic(root) // POST /api/auth/login: единственный маршрут /api без JWT
	root.Handle("/api/", authn.Middleware(jwt)(api))
	h := httpx.Chain(httpx.RequestID, httpx.Recover(log), httpx.AccessLog(log), httpx.CORS(cfg.CORSOrigins))(root)
	return &App{Handler: h, Cron: runner, Emitter: emitter}, nil
}

// healthz отвечает 200, если Postgres отвечает на ping, иначе 503 (агент не проверяется: ядро должно оставаться
// доступным).
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

// Run подключается к Postgres (с повторами, пока он стартует) и Redis, запускает AI-cron и, если задан KAFKA_BROKERS,
// публикатор outbox, и работает, пока ctx не отменён.
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
	var rdb redis.Cmdable // без Redis остаётся nil-интерфейсом
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

// Healthcheck выполняет GET url и падает на ответе не 200 (Docker HEALTHCHECK через `-healthcheck`).
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
