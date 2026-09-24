package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ninhdata/go-api-redis/internal/api"
	"github.com/ninhdata/go-api-redis/internal/auth"
	"github.com/ninhdata/go-api-redis/internal/config"
	"github.com/ninhdata/go-api-redis/internal/httpx"
	"github.com/ninhdata/go-api-redis/internal/notes"
	"github.com/ninhdata/go-api-redis/internal/redisclient"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if err := run(logger); err != nil {
		logger.Error("api exited with error", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	settings, err := config.Load()
	if err != nil {
		return err
	}
	if settings.UsesDevSecret() {
		logger.Warn("JWT_SECRET is using the development default, do not run this in production")
	}

	client, err := redisclient.New(ctx, redisclient.Options{
		Addr:     settings.RedisAddr,
		Password: settings.RedisPassword,
		DB:       settings.RedisDB,
	})
	if err != nil {
		return err
	}
	defer client.Close()

	authStore := auth.NewStore(client)
	tokens := auth.NewTokenManager(auth.TokenManagerConfig{
		Secret:     settings.JWTSecret,
		Issuer:     settings.JWTIssuer,
		Audience:   settings.JWTAudience,
		AccessTTL:  settings.AccessTTL,
		RefreshTTL: settings.RefreshTTL,
	})

	router := api.NewRouter(api.Dependencies{
		Config:      settings,
		Redis:       client,
		Tokens:      tokens,
		AuthHandler: auth.NewHandler(auth.NewService(authStore, tokens), authStore),
		Notes:       notes.NewHandler(notes.NewStore(client)),
		Logger:      logger,
	})

	return httpx.Serve(ctx, settings.APIAddr, router, logger)
}
