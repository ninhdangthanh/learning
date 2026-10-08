package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ninhdang/ws-chat/internal/auth"
	"github.com/ninhdang/ws-chat/internal/ws"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	webDir := flag.String("web", "web", "directory of the static test client")
	debug := flag.Bool("debug", false, "enable debug logs (ping/pong, frames)")
	devMode := flag.Bool("dev", false, "enable POST /dev/token to mint test JWTs (never in production)")
	origins := flag.String("origins", os.Getenv("WS_ALLOWED_ORIGINS"), "comma-separated extra origins allowed to open WebSocket (\"*\" allows all); defaults to $WS_ALLOWED_ORIGINS")
	flag.Parse()

	logger := newLogger(*debug)

	jwtSecret := []byte(os.Getenv("JWT_SECRET"))
	verifier, err := auth.NewVerifier(jwtSecret, auth.DefaultIssuer)
	if err != nil {
		logger.Error("invalid JWT_SECRET", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	hub := ws.NewHub(logger)
	go hub.Run(ctx)

	wsConfig := ws.DefaultConfig()
	wsConfig.AllowedOrigins = parseOrigins(*origins)

	mux := http.NewServeMux()
	mux.Handle("GET /ws", ws.NewHandler(wsConfig, hub, verifier, logger))
	mux.Handle("GET /debug/stats", statsHandler(hub, logger))
	if *devMode {
		issuer, err := auth.NewIssuer(jwtSecret, auth.DefaultIssuer)
		if err != nil {
			logger.Error("create dev token issuer", "error", err)
			os.Exit(1)
		}
		mux.Handle("POST /dev/token", auth.NewDevTokenHandler(issuer, logger))
		logger.Warn("dev mode enabled: POST /dev/token issues tokens for any user")
	}
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

func statsHandler(hub *ws.Hub, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(hub.Stats()); err != nil {
			logger.Warn("write stats response", "error", err)
		}
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
