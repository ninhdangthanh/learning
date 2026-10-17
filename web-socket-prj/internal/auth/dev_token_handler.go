package auth

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

const (
	maxDevTokenTTL     = time.Hour
	maxUserIDLength    = 128
	maxDevRequestBytes = 1024
)

type devTokenRequest struct {
	Subject    string `json:"sub"`
	TTLSeconds int    `json:"ttl_seconds"`
}

type devTokenResponse struct {
	AccessToken string    `json:"access_token"`
	TokenType   string    `json:"token_type"`
	ExpiresAt   time.Time `json:"expires_at"`
}

func NewDevTokenHandler(issuer *Issuer, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request devTokenRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxDevRequestBytes)).Decode(&request); err != nil {
			http.Error(w, "body must be JSON like {\"sub\":\"user-123\"}", http.StatusBadRequest)
			return
		}
		if request.Subject == "" || len(request.Subject) > maxUserIDLength {
			http.Error(w, "sub is required (max 128 bytes)", http.StatusBadRequest)
			return
		}
		ttl, ok := devTokenTTL(request.TTLSeconds)
		if !ok {
			http.Error(w, "ttl_seconds must be between 1 and 3600", http.StatusBadRequest)
			return
		}

		token, err := issuer.Issue(request.Subject, ttl)
		if err != nil {
			logger.Error("issue dev token", "error", err)
			http.Error(w, "cannot issue token", http.StatusInternalServerError)
			return
		}
		logger.Info("dev token issued", "user_id", request.Subject, "ttl", ttl)

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		response := devTokenResponse{AccessToken: token, TokenType: "Bearer", ExpiresAt: issuer.now().Add(ttl).UTC()}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			logger.Warn("write dev token response", "error", err)
		}
	}
}

func devTokenTTL(seconds int) (time.Duration, bool) {
	if seconds == 0 {
		return DefaultTokenTTL, true
	}
	ttl := time.Duration(seconds) * time.Second
	return ttl, seconds > 0 && ttl <= maxDevTokenTTL
}
