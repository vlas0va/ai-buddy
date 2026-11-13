// Команда mcp-mocks поднимает шесть мок-серверов MCP Авито (владелец B). Сборка зависимостей — в
// services/internal/mocks/app.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"aibuddy/services/internal/mocks/app"
)

func main() {
	healthcheck := flag.Bool("healthcheck", false, "GET /healthz on localhost and exit 0/1 (Docker HEALTHCHECK)")
	flag.Parse()
	cfg := app.LoadConfig(os.Getenv)
	if *healthcheck {
		if err := app.Healthcheck(fmt.Sprintf("http://127.0.0.1:%d/healthz", cfg.Port)); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := app.Run(ctx, cfg, log); err != nil {
		log.Error("mcp-mocks stopped", "err", err)
		os.Exit(1)
	}
}
