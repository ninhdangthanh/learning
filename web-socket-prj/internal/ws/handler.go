package ws

import (
	"log/slog"
	"net/http"
	"sync/atomic"

	"github.com/gorilla/websocket"
)

type Handler struct {
	cfg      Config
	logger   *slog.Logger
	upgrader websocket.Upgrader
	nextID   atomic.Uint64
	active   atomic.Int64
}

func NewHandler(cfg Config, logger *slog.Logger) *Handler {
	return &Handler{
		cfg:    cfg,
		logger: logger,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
		},
	}
}

func (h *Handler) ActiveConnections() int64 {
	return h.active.Load()
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Info("websocket upgrade rejected", "remote_addr", r.RemoteAddr, "error", err)
		return
	}

	c := newConnection(h.nextID.Add(1), conn, h.cfg, h.logger)
	h.active.Add(1)
	c.logger.Info("connection opened", "active", h.active.Load())

	c.run()

	remaining := h.active.Add(-1)
	c.logger.Info("connection closed", "active", remaining)
}
