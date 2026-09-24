package config

import (
	"errors"
	"os"
	"strconv"
	"time"

	"github.com/ninhdata/go-api-redis/internal/ratelimit"
)

const devSecret = "dev-secret-change-me"

type RateLimits struct {
	Register ratelimit.FixedWindowConfig
	Login    ratelimit.TokenBucketConfig
	API      ratelimit.SlidingWindowConfig
	Write    ratelimit.TokenBucketConfig
}

type Config struct {
	APIAddr string

	RedisAddr     string
	RedisPassword string
	RedisDB       int

	JWTSecret   []byte
	JWTIssuer   string
	JWTAudience string
	AccessTTL   time.Duration
	RefreshTTL  time.Duration

	RateLimits RateLimits
}

func Load() (Config, error) {
	config := Config{
		APIAddr: env("API_ADDR", ":8080"),

		RedisAddr:     env("REDIS_ADDR", "127.0.0.1:6379"),
		RedisPassword: env("REDIS_PASSWORD", ""),
		RedisDB:       envInt("REDIS_DB", 0),

		JWTSecret:   []byte(env("JWT_SECRET", devSecret)),
		JWTIssuer:   env("JWT_ISSUER", "go-api-redis"),
		JWTAudience: env("JWT_AUDIENCE", "go-api-redis-clients"),
		AccessTTL:   envDuration("ACCESS_TOKEN_TTL", 15*time.Minute),
		RefreshTTL:  envDuration("REFRESH_TOKEN_TTL", 7*24*time.Hour),

		RateLimits: RateLimits{
			Register: ratelimit.FixedWindowConfig{
				Limit:  int64(envInt("RL_REGISTER_LIMIT", 5)),
				Window: envDuration("RL_REGISTER_WINDOW", time.Hour),
			},
			Login: ratelimit.TokenBucketConfig{
				Capacity:        int64(envInt("RL_LOGIN_CAPACITY", 10)),
				RefillPerSecond: envFloat("RL_LOGIN_REFILL_PER_SECOND", 0.2),
			},
			API: ratelimit.SlidingWindowConfig{
				Limit:  int64(envInt("RL_API_LIMIT", 60)),
				Window: envDuration("RL_API_WINDOW", time.Minute),
			},
			Write: ratelimit.TokenBucketConfig{
				Capacity:        int64(envInt("RL_WRITE_CAPACITY", 20)),
				RefillPerSecond: envFloat("RL_WRITE_REFILL_PER_SECOND", 1),
			},
		},
	}

	return config, config.validate()
}

func (c Config) UsesDevSecret() bool {
	return string(c.JWTSecret) == devSecret
}

func (c Config) validate() error {
	if len(c.JWTSecret) < 16 {
		return errors.New("JWT_SECRET must be at least 16 characters")
	}
	if c.AccessTTL <= 0 || c.RefreshTTL <= c.AccessTTL {
		return errors.New("REFRESH_TOKEN_TTL must be longer than ACCESS_TOKEN_TTL")
	}
	return nil
}

func env(name, fallback string) string {
	if value, ok := os.LookupEnv(name); ok && value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) int {
	value, err := strconv.Atoi(env(name, ""))
	if err != nil {
		return fallback
	}
	return value
}

func envFloat(name string, fallback float64) float64 {
	value, err := strconv.ParseFloat(env(name, ""), 64)
	if err != nil {
		return fallback
	}
	return value
}

func envDuration(name string, fallback time.Duration) time.Duration {
	value, err := time.ParseDuration(env(name, ""))
	if err != nil {
		return fallback
	}
	return value
}
