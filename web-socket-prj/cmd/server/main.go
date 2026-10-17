package main

import (
	"context"
	"encoding/json"
	"errors"
	"expvar"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ninhdang/ws-chat/internal/auth"
	"github.com/ninhdang/ws-chat/internal/membership"
	"github.com/ninhdang/ws-chat/internal/ws"
)

func main() {
	defaults := ws.DefaultConfig()
	addr := flag.String("addr", ":8080", "HTTP listen address")
	webDir := flag.String("web", "web", "directory of the static test client")
	debug := flag.Bool("debug", false, "enable debug logs (ping/pong, frames)")
	devMode := flag.Bool("dev", false, "enable POST /dev/token and POST /admin/rooms/{room}/revoke (never in production)")
	membershipsPath := flag.String("memberships", "data/memberships.json", "JSON file mapping room_id to member user ids")
	origins := flag.String("origins", os.Getenv("WS_ALLOWED_ORIGINS"), "comma-separated extra origins allowed to open WebSocket (\"*\" allows all); defaults to $WS_ALLOWED_ORIGINS")
	pingPeriod := flag.Duration("ping-period", defaults.PingPeriod, "interval between WebSocket ping frames; overrides $WS_PING_PERIOD")
	pongWait := flag.Duration("pong-wait", defaults.PongWait, "max silence before a peer is considered dead; overrides $WS_PONG_WAIT")
	writeWait := flag.Duration("write-wait", defaults.WriteWait, "deadline for a single frame write; overrides $WS_WRITE_WAIT")
	flag.Parse()

	logger := newLogger(*debug)

	wsConfig := defaults
	wsConfig.AllowedOrigins = parseOrigins(*origins)
	if err := applyTimingSettings(&wsConfig, *pingPeriod, *pongWait, *writeWait); err != nil {
		logger.Error("invalid timing settings", "error", err)
		os.Exit(1)
	}
	if err := wsConfig.Validate(); err != nil {
		logger.Error("invalid websocket config", "error", err)
		os.Exit(1)
	}

	jwtSecret := []byte(os.Getenv("JWT_SECRET"))
	verifier, err := auth.NewVerifier(jwtSecret, auth.DefaultIssuer)
	if err != nil {
		logger.Error("invalid JWT_SECRET", "error", err)
		os.Exit(1)
	}

	memberships, err := membership.LoadFromFile(*membershipsPath)
	if err != nil {
		logger.Error("load memberships", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	hub := ws.NewHub(logger)
	go hub.Run(ctx)

	metrics := ws.NewMetrics()
	metrics.Publish()

	mux := http.NewServeMux()
	mux.Handle("GET /ws", ws.NewHandler(wsConfig, hub, verifier, memberships, metrics, logger))
	mux.Handle("GET /debug/stats", statsHandler(hub, logger))
	mux.Handle("GET /debug/vars", expvar.Handler())
	if *devMode {
		issuer, err := auth.NewIssuer(jwtSecret, auth.DefaultIssuer)
		if err != nil {
			logger.Error("create dev token issuer", "error", err)
			os.Exit(1)
		}
		mux.Handle("POST /dev/token", auth.NewDevTokenHandler(issuer, logger))
		mux.Handle("POST /admin/rooms/{"+ws.RevokeRoomPathValue+"}/revoke", ws.NewRevokeHandler(memberships, hub, logger))
		logger.Warn("dev mode enabled: POST /dev/token issues tokens for any user, POST /admin/rooms/{room}/revoke is unauthenticated")
	}
	mux.Handle("GET /", http.FileServer(http.Dir(*webDir)))

	server := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.Info("server listening", "addr", *addr, "allowed_origins", wsConfig.AllowedOrigins,
			"ping_period", wsConfig.PingPeriod, "pong_wait", wsConfig.PongWait, "write_wait", wsConfig.WriteWait)
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

func applyTimingSettings(cfg *ws.Config, pingPeriod, pongWait, writeWait time.Duration) error {
	explicit := make(map[string]bool)
	flag.Visit(func(f *flag.Flag) { explicit[f.Name] = true })

	settings := []struct {
		flagName, envName string
		flagValue         time.Duration
		target            *time.Duration
	}{
		{"ping-period", "WS_PING_PERIOD", pingPeriod, &cfg.PingPeriod},
		{"pong-wait", "WS_PONG_WAIT", pongWait, &cfg.PongWait},
		{"write-wait", "WS_WRITE_WAIT", writeWait, &cfg.WriteWait},
	}
	for _, setting := range settings {
		*setting.target = setting.flagValue
		raw, fromEnv := os.LookupEnv(setting.envName)
		if explicit[setting.flagName] || !fromEnv {
			continue
		}
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("%s=%q: %w", setting.envName, raw, err)
		}
		*setting.target = parsed
	}
	return nil
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
