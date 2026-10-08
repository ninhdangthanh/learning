package ratelimit

import (
	"log/slog"
	"math"
	"net/http"
	"strconv"

	"github.com/ninhdata/go-api-redis/internal/httpx"
)

type KeyFunc func(*http.Request) string

func Middleware(limiter Limiter, keyFunc KeyFunc, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := keyFunc(r)
			result, err := limiter.Allow(r.Context(), key)
			if err != nil {
				logger.Error("rate limiter unavailable, allowing request",
					"limiter", limiter.Name(), "key", key, "error", err)
				next.ServeHTTP(w, r)
				return
			}

			writeHeaders(w, limiter.Name(), result)

			if !result.Allowed {
				httpx.WriteError(w, http.StatusTooManyRequests, "rate_limited",
					"Too many requests. Please retry later.")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func writeHeaders(w http.ResponseWriter, policy string, result Result) {
	header := w.Header()
	header.Set("RateLimit-Policy", policy)
	header.Set("RateLimit-Limit", strconv.FormatInt(result.Limit, 10))
	header.Set("RateLimit-Remaining", strconv.FormatInt(result.Remaining, 10))
	header.Set("RateLimit-Reset", strconv.Itoa(secondsCeil(result.ResetAfter.Seconds())))
	if !result.Allowed {
		header.Set("Retry-After", strconv.Itoa(secondsCeil(result.RetryAfter.Seconds())))
	}
}

func secondsCeil(seconds float64) int {
	value := int(math.Ceil(seconds))
	if value < 1 {
		return 1
	}
	return value
}
