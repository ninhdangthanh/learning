package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ninhdang/ws-chat/internal/ws"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	webDir := flag.String("web", "web", "directory of the static test client")
	debug := flag.Bool("debug", false, "enable debug logs (ping/pong, frames)")
	origins := flag.String("origins", os.Getenv("WS_ALLOWED_ORIGINS"), "comma-separated extra origins allowed to open WebSocket (\"*\" allows all); defaults to $WS_ALLOWED_ORIGINS")
	flag.Parse()

	logger := newLogger(*debug)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	hub := ws.NewHub(logger)
	go hub.Run(ctx)

	wsConfig := ws.DefaultConfig()
	wsConfig.AllowedOrigins = parseOrigins(*origins)

	mux := http.NewServeMux()
	mux.Handle("GET /ws", ws.NewHandler(wsConfig, hub, logger))
	mux.Handle("GET /", http.FileServer(http.Dir(*webDir)))

	server := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.Info("server listening", "addr", *addr, "allowed_origins", wsConfig.AllowedOrigins)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server stopped", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown", "error", err)
	}
}

func newLogger(debug bool) *slog.Logger {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}

func parseOrigins(raw string) []string {
	var origins []string
	for _, origin := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(origin); trimmed != "" {
			origins = append(origins, trimmed)
		}
	}
	return origins
}
