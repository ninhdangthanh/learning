package ws

import (
	"context"
	"log/slog"

	"github.com/gorilla/websocket"
)

type Hub struct {
	logger        *slog.Logger
	clients       map[*Client]struct{}
	register      chan *Client
	unregister    chan *Client
	broadcast     chan []byte
	countRequests chan chan int
	stopped       chan struct{}
}

func NewHub(logger *slog.Logger) *Hub {
	return &Hub{
		logger:        logger,
		clients:       make(map[*Client]struct{}),
		register:      make(chan *Client),
		unregister:    make(chan *Client),
		broadcast:     make(chan []byte),
		countRequests: make(chan chan int),
		stopped:       make(chan struct{}),
	}
}

func (h *Hub) Run(ctx context.Context) {
	defer close(h.stopped)
	for {
		select {
		case <-ctx.Done():
			return
		case client := <-h.register:
			h.clients[client] = struct{}{}
			client.logger.Debug("client registered", "clients", len(h.clients))
		case client := <-h.unregister:
			h.remove(client)
		case payload := <-h.broadcast:
			h.fanOut(payload)
		case reply := <-h.countRequests:
			reply <- len(h.clients)
		}
	}
}

func (h *Hub) Register(client *Client) bool {
	select {
	case h.register <- client:
		return true
	case <-h.stopped:
		return false
	}
}

func (h *Hub) Unregister(client *Client) {
	select {
	case h.unregister <- client:
	case <-h.stopped:
	}
}

func (h *Hub) Broadcast(payload []byte) {
	select {
	case h.broadcast <- payload:
	case <-h.stopped:
	}
}

func (h *Hub) ClientCount() int {
	reply := make(chan int, 1)
	select {
	case h.countRequests <- reply:
		return <-reply
	case <-h.stopped:
		return 0
	}
}

func (h *Hub) remove(client *Client) {
	if _, ok := h.clients[client]; !ok {
		return
	}
	delete(h.clients, client)
	client.logger.Debug("client unregistered", "clients", len(h.clients))
}

func (h *Hub) fanOut(payload []byte) {
	frame := outboundFrame{messageType: websocket.TextMessage, payload: payload}
	for client := range h.clients {
		if client.trySend(frame) {
			continue
		}
		delete(h.clients, client)
		if client.isStopped() {
			continue
		}
		client.logger.Warn("slow client kicked", "send_buffer", cap(client.send))
		client.stop(websocket.ClosePolicyViolation, "send buffer full")
	}
}
