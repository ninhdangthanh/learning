package auth

import (
	"errors"
	"net/http"
	"strings"

	"github.com/ninhdata/go-api-redis/internal/httpx"
)

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	prefix := "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(header[len(prefix):])
}

func RequireAuth(tokens *TokenManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := bearerToken(r)
			if raw == "" {
				httpx.WriteError(w, http.StatusUnauthorized, "missing_token", "Authorization header is required.")
				return
			}

			claims, err := tokens.Parse(raw, TokenTypeAccess)
			if errors.Is(err, ErrExpiredToken) {
				httpx.WriteError(w, http.StatusUnauthorized, "token_expired", "Access token has expired.")
				return
			}
			if err != nil {
				httpx.WriteError(w, http.StatusUnauthorized, "invalid_token", "Access token is not valid.")
				return
			}

			identity := Identity{UserID: claims.Subject, Role: claims.Role}
			next.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(), identity)))
		})
	}
}
