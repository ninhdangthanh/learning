package ws

import (
	"log/slog"
	"net/http"
	"sync/atomic"

	"github.com/gorilla/websocket"
)

type Handler struct {
	cfg      Config
	hub      *Hub
	logger   *slog.Logger
	upgrader websocket.Upgrader
	nextID   atomic.Uint64
	active   atomic.Int64
}

func NewHandler(cfg Config, hub *Hub, logger *slog.Logger) *Handler {
	return &Handler{
		cfg:    cfg,
		hub:    hub,
		logger: logger,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin:     newOriginChecker(cfg.AllowedOrigins),
		},
	}
}

func (h *Handler) ActiveConnections() int64 {
	return h.active.Load()
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Info("websocket upgrade rejected", "remote_addr", r.RemoteAddr, "origin", r.Header.Get("Origin"), "error", err)
		return
	}

	client := newClient(h.nextID.Add(1), h.hub, conn, h.cfg, h.logger)
	if !h.hub.Register(client) {
		client.closeWith(websocket.CloseGoingAway, "server shutting down")
	}
	h.active.Add(1)
	client.logger.Info("connection opened", "active", h.active.Load())

	client.run()

	remaining := h.active.Add(-1)
	client.logger.Info("connection closed", "active", remaining)
}
