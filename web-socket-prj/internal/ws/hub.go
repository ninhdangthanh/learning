package ws

import (
	"context"
	"log/slog"

	"github.com/gorilla/websocket"
)

type commandKind int

const (
	commandRegister commandKind = iota
	commandUnregister
	commandJoin
	commandLeave
	commandPublish
)

type hubCommand struct {
	kind    commandKind
	client  *Client
	roomID  string
	payload []byte
}

type Stats struct {
	Clients int `json:"clients"`
	Rooms   int `json:"rooms"`
}

type Hub struct {
	logger        *slog.Logger
	clients       map[*Client]struct{}
	rooms         map[string]map[*Client]struct{}
	commands      chan hubCommand
	statsRequests chan chan Stats
	stopped       chan struct{}
}

func NewHub(logger *slog.Logger) *Hub {
	return &Hub{
		logger:        logger,
		clients:       make(map[*Client]struct{}),
		rooms:         make(map[string]map[*Client]struct{}),
		commands:      make(chan hubCommand),
		statsRequests: make(chan chan Stats),
		stopped:       make(chan struct{}),
	}
}

func (h *Hub) Run(ctx context.Context) {
	defer close(h.stopped)
	for {
		select {
		case <-ctx.Done():
			return
		case cmd := <-h.commands:
			h.handle(cmd)
		case reply := <-h.statsRequests:
			reply <- Stats{Clients: len(h.clients), Rooms: len(h.rooms)}
		}
	}
}

func (h *Hub) Register(client *Client) bool {
	return h.submit(hubCommand{kind: commandRegister, client: client})
}

func (h *Hub) Unregister(client *Client) {
	h.submit(hubCommand{kind: commandUnregister, client: client})
}

func (h *Hub) Join(client *Client, roomID string) {
	h.submit(hubCommand{kind: commandJoin, client: client, roomID: roomID})
}

func (h *Hub) Leave(client *Client, roomID string) {
	h.submit(hubCommand{kind: commandLeave, client: client, roomID: roomID})
}

func (h *Hub) Publish(client *Client, roomID string, payload []byte) {
	h.submit(hubCommand{kind: commandPublish, client: client, roomID: roomID, payload: payload})
}

func (h *Hub) Stats() Stats {
	reply := make(chan Stats, 1)
	select {
	case h.statsRequests <- reply:
		return <-reply
	case <-h.stopped:
		return Stats{}
	}
}

func (h *Hub) submit(cmd hubCommand) bool {
	select {
	case h.commands <- cmd:
		return true
	case <-h.stopped:
		return false
	}
}

func (h *Hub) handle(cmd hubCommand) {
	if cmd.kind == commandRegister {
		h.clients[cmd.client] = struct{}{}
		cmd.client.logger.Debug("client registered", "clients", len(h.clients))
		return
	}
	if _, registered := h.clients[cmd.client]; !registered {
		return
	}

	switch cmd.kind {
	case commandUnregister:
		h.remove(cmd.client)
	case commandJoin:
		h.join(cmd.client, cmd.roomID)
	case commandLeave:
		h.leave(cmd.client, cmd.roomID)
	case commandPublish:
		h.publish(cmd.client, cmd.roomID, cmd.payload)
	}
}

func (h *Hub) join(client *Client, roomID string) {
	if _, joined := client.rooms[roomID]; !joined {
		if len(client.rooms) >= MaxRoomsPerClient {
			h.reply(client, newRequestError(ErrorCodeTooManyRooms, "room limit reached", inboundEnvelope{Type: messageTypeJoinRoom, RoomID: roomID}))
			return
		}
		h.attach(client, roomID)
	}
	h.reply(client, newAck(messageTypeJoined, roomID))
}

func (h *Hub) leave(client *Client, roomID string) {
	if _, joined := client.rooms[roomID]; !joined {
		h.reply(client, newRequestError(ErrorCodeNotInRoom, "not a member of this room", inboundEnvelope{Type: messageTypeLeaveRoom, RoomID: roomID}))
		return
	}
	h.detach(client, roomID)
	h.reply(client, newAck(messageTypeLeft, roomID))
}

func (h *Hub) publish(client *Client, roomID string, payload []byte) {
	if _, joined := client.rooms[roomID]; !joined {
		h.reply(client, newRequestError(ErrorCodeNotInRoom, "join the room before sending messages", inboundEnvelope{Type: messageTypeChat, RoomID: roomID}))
		return
	}
	frame := outboundFrame{messageType: websocket.TextMessage, payload: payload}
	for member := range h.rooms[roomID] {
		h.deliver(member, frame)
	}
}

func (h *Hub) attach(client *Client, roomID string) {
	members, exists := h.rooms[roomID]
	if !exists {
		members = make(map[*Client]struct{})
		h.rooms[roomID] = members
		h.logger.Debug("room created", "room_id", roomID, "rooms", len(h.rooms))
	}
	members[client] = struct{}{}
	client.rooms[roomID] = struct{}{}
	client.logger.Debug("joined room", "room_id", roomID, "members", len(members))
}

func (h *Hub) detach(client *Client, roomID string) {
	delete(client.rooms, roomID)
	members := h.rooms[roomID]
	delete(members, client)
	client.logger.Debug("left room", "room_id", roomID, "members", len(members))
	if len(members) == 0 {
		delete(h.rooms, roomID)
		h.logger.Debug("room removed", "room_id", roomID, "rooms", len(h.rooms))
	}
}

func (h *Hub) remove(client *Client) {
	if _, registered := h.clients[client]; !registered {
		return
	}
	for roomID := range client.rooms {
		h.detach(client, roomID)
	}
	delete(h.clients, client)
	client.logger.Debug("client unregistered", "clients", len(h.clients))
}

func (h *Hub) reply(client *Client, value any) {
	frame, err := encodeTextFrame(value)
	if err != nil {
		client.logger.Error("encode hub reply", "error", err)
		return
	}
	h.deliver(client, frame)
}

func (h *Hub) deliver(client *Client, frame outboundFrame) {
	if client.trySend(frame) {
		return
	}
	h.remove(client)
	if client.isStopped() {
		return
	}
	client.logger.Warn("slow client kicked", "send_buffer", cap(client.send))
	client.stop(websocket.ClosePolicyViolation, "send buffer full")
}
