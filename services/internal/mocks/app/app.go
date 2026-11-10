// Package app wires mcp-mocks: six independent mock Avito MCP servers served by one binary at /mcp/<server>
// (absence, approvals, access, directory, checklist, kb). Each server owns its DB schema and tool set, so it can be
// moved to its own process or replaced by a real Avito API by changing a URL in the agent config (R23, R56).
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"aibuddy/services/internal/mocks/absence"
	"aibuddy/services/internal/mocks/access"
	"aibuddy/services/internal/mocks/approvals"
	"aibuddy/services/internal/mocks/checklist"
	"aibuddy/services/internal/mocks/directory"
	"aibuddy/services/internal/mocks/kb"
	"aibuddy/services/internal/platform/config"
	"aibuddy/services/internal/platform/httpx"
	"aibuddy/services/internal/platform/mcpx"
	"aibuddy/services/internal/platform/pg"
)

// Config holds mcp-mocks settings (docs/backend/IMPLEMENTATION.md §7).
type Config struct {
	Port               int
	PostgresURL        string
	JWTSecret          string
	ConfirmationSecret string
	KBDir              string
}

// LoadConfig reads configuration from getenv; Validate reports missing secrets.
func LoadConfig(getenv func(string) string) Config {
	env := config.New(getenv)
	return Config{
		Port:               env.Int("MCP_MOCKS_PORT", 8100),
		PostgresURL:        env.String("POSTGRES_URL", "postgres://buddy:buddy@postgres:5432/buddy?sslmode=disable"),
		JWTSecret:          env.String("JWT_SECRET", ""),
		ConfirmationSecret: env.String("CONFIRMATION_HMAC_SECRET", ""),
		KBDir:              env.String("KB_DIR", "/app/data/kb"),
	}
}

// Validate fails when a required secret is missing: the service must not start with empty secrets.
func (c Config) Validate() error {
	var missing []string
	if c.JWTSecret == "" {
		missing = append(missing, "JWT_SECRET")
	}
	if c.ConfirmationSecret == "" {
		missing = append(missing, "CONFIRMATION_HMAC_SECRET")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required environment variables: %v", missing)
	}
	return nil
}

// registrars lists every mock server and its registration function, in a stable order.
var registrars = []struct {
	name     string
	register func(*mcpx.Server, *pgxpool.Pool) error
}{
	{"absence", absence.Register},
	{"approvals", approvals.Register},
	{"access", access.Register},
	{"directory", directory.Register},
	{"checklist", checklist.Register},
	{"kb", kb.Register},
}

// NewHandler builds the HTTP handler: /healthz plus /mcp/<server> for all six servers.
func NewHandler(cfg Config, pool *pgxpool.Pool, log *slog.Logger) (http.Handler, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := pool.Ping(ctx); err != nil {
			httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "degraded", "service": "mcp-mocks"})
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "mcp-mocks"})
	})
	mcfg := mcpx.Config{JWTSecret: []byte(cfg.JWTSecret), ConfirmationSecret: []byte(cfg.ConfirmationSecret), Logger: log}
	servers := make([]*mcpx.Server, 0, len(registrars))
	for _, r := range registrars {
		srv, err := mcpx.NewServer(r.name, mcfg)
		if err != nil {
			return nil, fmt.Errorf("mcp server %s: %w", r.name, err)
		}
		if err := r.register(srv, pool); err != nil {
			return nil, fmt.Errorf("register %s: %w", r.name, err)
		}
		servers = append(servers, srv)
	}
	if err := mcpx.Mount(mux, servers...); err != nil {
		return nil, err
	}
	return httpx.Chain(httpx.RequestID, httpx.Recover(log), httpx.AccessLog(log))(mux), nil
}

// Run connects to Postgres, loads the knowledge base from KB_DIR and serves until ctx is cancelled.
func Run(ctx context.Context, cfg Config, log *slog.Logger) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	pool, err := pg.Open(ctx, cfg.PostgresURL, pg.Options{})
	if err != nil {
		return err
	}
	defer pool.Close()
	n, err := kb.LoadDir(ctx, pool, cfg.KBDir)
	if err != nil {
		return fmt.Errorf("load knowledge base from %s: %w", cfg.KBDir, err)
	}
	log.Info("knowledge base loaded", "dir", cfg.KBDir, "documents", n)
	h, err := NewHandler(cfg, pool, log)
	if err != nil {
		return err
	}
	return serve(ctx, fmt.Sprintf(":%d", cfg.Port), h, log)
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
