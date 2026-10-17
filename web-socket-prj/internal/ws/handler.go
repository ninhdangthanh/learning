package ws

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ninhdang/ws-chat/internal/auth"
)

const (
	accessTokenQueryParam = "access_token"
	bearerPrefix          = "bearer "
)

type TokenVerifier interface {
	Verify(token string) (auth.Identity, error)
}

type MembershipChecker interface {
	IsMember(ctx context.Context, userID, roomID string) (bool, error)
}

type Handler struct {
	cfg         Config
	hub         *Hub
	verifier    TokenVerifier
	memberships MembershipChecker
	metrics     *Metrics
	logger      *slog.Logger
	upgrader    websocket.Upgrader
	nextID      atomic.Uint64
}

func NewHandler(cfg Config, hub *Hub, verifier TokenVerifier, memberships MembershipChecker, metrics *Metrics, logger *slog.Logger) *Handler {
	return &Handler{
		cfg:         cfg,
		hub:         hub,
		verifier:    verifier,
		memberships: memberships,
		metrics:     metrics,
		logger:      logger,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin:     newOriginChecker(cfg.AllowedOrigins),
		},
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	identity, err := h.verifier.Verify(tokenFromRequest(r))
	if err != nil {
		h.rejectUnauthorized(w, r, err)
		return
	}

	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Info("websocket upgrade rejected", "remote_addr", r.RemoteAddr, "origin", r.Header.Get("Origin"), "user_id", identity.UserID, "error", err)
		return
	}

	deps := clientDeps{hub: h.hub, memberships: h.memberships, cfg: h.cfg, metrics: h.metrics, logger: h.logger}
	client := newClient(r.Context(), h.nextID.Add(1), conn, identity, deps)
	if !h.hub.Register(client) {
		client.closeWith(websocket.CloseGoingAway, "server shutting down", reasonServerShutdown)
	}
	h.metrics.ActiveConnections.Add(1)
	openedAt := time.Now()
	client.logger.Info("connection opened", "active", h.metrics.ActiveConnections.Value(), "token_expires_at", identity.ExpiresAt)

	client.run()

	h.metrics.ActiveConnections.Add(-1)
	h.metrics.recordDisconnect(client.disconnect)
	client.logger.Info("connection closed", "reason", client.disconnect, "duration", time.Since(openedAt).Round(time.Millisecond), "active", h.metrics.ActiveConnections.Value())
}

func (h *Handler) rejectUnauthorized(w http.ResponseWriter, r *http.Request, err error) {
	h.logger.Info("websocket auth rejected", "remote_addr", r.RemoteAddr, "path", r.URL.Path, "error", err)
	w.Header().Set("WWW-Authenticate", bearerChallenge(err))
	http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
}

func bearerChallenge(err error) string {
	switch {
	case errors.Is(err, auth.ErrTokenMissing):
		return `Bearer realm="ws-chat"`
	case errors.Is(err, auth.ErrTokenExpired):
		return `Bearer realm="ws-chat", error="invalid_token", error_description="token expired"`
	default:
		return `Bearer realm="ws-chat", error="invalid_token"`
	}
}

func tokenFromRequest(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if len(header) > len(bearerPrefix) && strings.EqualFold(header[:len(bearerPrefix)], bearerPrefix) {
		return strings.TrimSpace(header[len(bearerPrefix):])
	}
	return r.URL.Query().Get(accessTokenQueryParam)
}
