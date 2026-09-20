package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/ninhdata/go-api-redis/internal/auth"
	"github.com/ninhdata/go-api-redis/internal/config"
	"github.com/ninhdata/go-api-redis/internal/httpx"
	"github.com/ninhdata/go-api-redis/internal/notes"
	"github.com/ninhdata/go-api-redis/internal/ratelimit"
	"github.com/redis/go-redis/v9"
)

type Dependencies struct {
	Config      config.Config
	Redis       *redis.Client
	AuthStore   *auth.Store
	Tokens      *auth.TokenManager
	AuthHandler *auth.Handler
	Notes       *notes.Handler
	Logger      *slog.Logger
}

func keyByIP(r *http.Request) string {
	return "ip:" + httpx.ClientIP(r)
}

func keyByIdentity(r *http.Request) string {
	if userID := auth.UserIDFrom(r.Context()); userID != "" {
		return "user:" + userID
	}
	return keyByIP(r)
}

func NewRouter(deps Dependencies) http.Handler {
	limits := deps.Config.RateLimits

	registerLimiter := ratelimit.NewFixedWindow(deps.Redis, "register", limits.Register)
	loginLimiter := ratelimit.NewTokenBucket(deps.Redis, "login", limits.Login)
	refreshLimiter := ratelimit.NewTokenBucket(deps.Redis, "refresh", limits.Login)
	apiLimiter := ratelimit.NewSlidingWindow(deps.Redis, "api", limits.API)
	writeLimiter := ratelimit.NewTokenBucket(deps.Redis, "write", limits.Write)

	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(middleware.RealIP)
	router.Use(middleware.Recoverer)
	router.Use(middleware.Timeout(15 * time.Second))
	router.Use(requestLogger(deps.Logger))

	router.Get("/healthz", healthz(deps.Redis))

	router.Route("/auth", func(r chi.Router) {
		r.With(ratelimit.Middleware(registerLimiter, keyByIP, deps.Logger)).
			Post("/register", deps.AuthHandler.Register)
		r.With(ratelimit.Middleware(loginLimiter, keyByIP, deps.Logger)).
			Post("/login", deps.AuthHandler.Login)
		r.With(ratelimit.Middleware(refreshLimiter, keyByIP, deps.Logger)).
			Post("/refresh", deps.AuthHandler.Refresh)

		r.Group(func(private chi.Router) {
			private.Use(auth.RequireAuth(deps.AuthStore, deps.Tokens))
			private.Post("/logout", deps.AuthHandler.Logout)
			private.Get("/me", deps.AuthHandler.Me)
		})
	})

	router.Route("/api", func(r chi.Router) {
		r.Use(auth.RequireAuth(deps.AuthStore, deps.Tokens))
		r.Use(ratelimit.Middleware(apiLimiter, keyByIdentity, deps.Logger))

		r.Mount("/notes", deps.Notes.Routes(
			ratelimit.Middleware(writeLimiter, keyByIdentity, deps.Logger)))
	})

	return router
}

func healthz(client *redis.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := client.Ping(r.Context()).Err(); err != nil {
			httpx.WriteError(w, http.StatusServiceUnavailable, "redis_unavailable", "Redis is not reachable.")
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func requestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started := time.Now()
			wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			next.ServeHTTP(wrapped, r)

			logger.Info("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", wrapped.Status(),
				"duration_ms", time.Since(started).Milliseconds(),
				"request_id", middleware.GetReqID(r.Context()),
				"ip", httpx.ClientIP(r))
		})
	}
}
