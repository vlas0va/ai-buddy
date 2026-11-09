// Package app wires the core service: configuration, dependencies and HTTP routes.
// Stage-0 skeleton: only /healthz. Real wiring is added during integration (see docs/backend/IMPLEMENTATION.md).
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"
)

// Config holds service settings read from the environment.
type Config struct {
	Port int
}

// LoadConfig reads configuration using getenv (os.Getenv in production).
func LoadConfig(getenv func(string) string) Config {
	port := 8080
	if v, err := strconv.Atoi(getenv("CORE_PORT")); err == nil && v > 0 && v < 65536 {
		port = v
	}
	return Config{Port: port}
}

// Handler returns the HTTP handler of the service.
func Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","service":"core"}`))
	})
	return mux
}

// Run serves until ctx is cancelled.
func Run(ctx context.Context, cfg Config, log *slog.Logger) error {
	srv := &http.Server{Addr: fmt.Sprintf(":%d", cfg.Port), Handler: Handler(), ReadHeaderTimeout: 5 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("listening", "port", cfg.Port)
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

// Healthcheck performs GET url and fails on a non-200 answer.
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

var _ = os.Getenv
