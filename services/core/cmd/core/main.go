// Command core is the AI-buddy core API.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"aibuddy/core/internal/config"
	"aibuddy/core/internal/httpserver"
)

func main() {
	hc := flag.Bool("healthcheck", false, "GET /healthz on localhost and exit 0/1")
	flag.Parse()
	cfg := config.Load(os.Getenv)
	if *hc {
		if err := httpserver.Healthcheck(fmt.Sprintf("http://127.0.0.1:%d/healthz", cfg.Port)); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := httpserver.Run(ctx, fmt.Sprintf(":%d", cfg.Port), httpserver.NewHandler(log), log); err != nil {
		log.Error("server failed", "err", err)
		os.Exit(1)
	}
}
