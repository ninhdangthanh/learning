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
	commandRevoke
	commandSendToUser
)

type hubCommand struct {
	kind    commandKind
	client  *Client
	userID  string
	roomID  string
	payload []byte
	kicked  chan int
}

type Stats struct {
	Clients            int            `json:"clients"`
	Users              int            `json:"users"`
	Rooms              int            `json:"rooms"`
	ConnectionsPerUser map[string]int `json:"connections_per_user"`
}

type Hub struct {
	logger        *slog.Logger
	clients       map[*Client]struct{}
	users         map[string]map[*Client]struct{}
	rooms         map[string]map[*Client]struct{}
	commands      chan hubCommand
	statsRequests chan chan Stats
	stopped       chan struct{}
}

func NewHub(logger *slog.Logger) *Hub {
	return &Hub{
		logger:        logger,
		clients:       make(map[*Client]struct{}),
		users:         make(map[string]map[*Client]struct{}),
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
			reply <- h.snapshot()
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

func (h *Hub) Revoke(userID, roomID string) (int, bool) {
	kicked := make(chan int, 1)
	if !h.submit(hubCommand{kind: commandRevoke, userID: userID, roomID: roomID, kicked: kicked}) {
		return 0, false
	}
	return <-kicked, true
}

func (h *Hub) SendToUser(userID string, payload []byte) bool {
	return h.submit(hubCommand{kind: commandSendToUser, userID: userID, payload: payload})
}

func (h *Hub) Stats() Stats {
	reply := make(chan Stats, 1)
	select {
	case h.statsRequests <- reply:
		return <-reply
	case <-h.stopped:
		return Stats{ConnectionsPerUser: map[string]int{}}
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

func (h *Hub) snapshot() Stats {
	connectionsPerUser := make(map[string]int, len(h.users))
	for userID, conns := range h.users {
		connectionsPerUser[userID] = len(conns)
	}
	return Stats{Clients: len(h.clients), Users: len(h.users), Rooms: len(h.rooms), ConnectionsPerUser: connectionsPerUser}
}

func (h *Hub) handle(cmd hubCommand) {
	switch cmd.kind {
	case commandRegister:
		h.register(cmd.client)
		return
	case commandRevoke:
		cmd.kicked <- h.revoke(cmd.userID, cmd.roomID)
		return
	case commandSendToUser:
		h.sendToUser(cmd.userID, outboundFrame{messageType: websocket.TextMessage, payload: cmd.payload})
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

func (h *Hub) register(client *Client) {
	conns := h.users[client.userID]
	if len(conns) >= MaxConnectionsPerUser {
		client.logger.Warn("connection limit reached, rejecting new connection", "limit", MaxConnectionsPerUser)
		client.stop(CloseTooManyConnections, "too many connections", reasonTooManyConnections)
		return
	}
	if conns == nil {
		conns = make(map[*Client]struct{})
		h.users[client.userID] = conns
	}
	conns[client] = struct{}{}
	h.clients[client] = struct{}{}
	client.logger.Debug("client registered", "clients", len(h.clients), "user_connections", len(conns))
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
	h.broadcast(roomID, outboundFrame{messageType: websocket.TextMessage, payload: payload}, "")
}

func (h *Hub) revoke(userID, roomID string) int {
	var revoked []*Client
	for conn := range h.users[userID] {
		if _, joined := conn.rooms[roomID]; joined {
			revoked = append(revoked, conn)
		}
	}
	for _, conn := range revoked {
		h.detach(conn, roomID)
		conn.logger.Info("membership revoked, removed from room", "room_id", roomID)
		h.reply(conn, newAck(messageTypeRemoved, roomID))
	}
	return len(revoked)
}

func (h *Hub) sendToUser(userID string, frame outboundFrame) {
	for conn := range h.users[userID] {
		h.deliver(conn, frame)
	}
}

func (h *Hub) attach(client *Client, roomID string) {
	firstDeviceInRoom := !h.userHasConnectionInRoom(client.userID, roomID)
	members, exists := h.rooms[roomID]
	if !exists {
		members = make(map[*Client]struct{})
		h.rooms[roomID] = members
		h.logger.Debug("room created", "room_id", roomID, "rooms", len(h.rooms))
	}
	members[client] = struct{}{}
	client.rooms[roomID] = struct{}{}
	client.logger.Debug("joined room", "room_id", roomID, "members", len(members))
	if firstDeviceInRoom {
		h.announcePresence(roomID, client.userID, presenceOnline)
	}
}

func (h *Hub) detach(client *Client, roomID string) {
	if _, joined := client.rooms[roomID]; !joined {
		return
	}
	delete(client.rooms, roomID)
	members := h.rooms[roomID]
	delete(members, client)
	client.logger.Debug("left room", "room_id", roomID, "members", len(members))
	if len(members) == 0 {
		delete(h.rooms, roomID)
		h.logger.Debug("room removed", "room_id", roomID, "rooms", len(h.rooms))
		return
	}
	if !h.userHasConnectionInRoom(client.userID, roomID) {
		h.announcePresence(roomID, client.userID, presenceOffline)
	}
}

func (h *Hub) remove(client *Client) {
	if _, registered := h.clients[client]; !registered {
		return
	}
	delete(h.clients, client)
	h.forgetUserConnection(client)
	for roomID := range client.rooms {
		h.detach(client, roomID)
	}
	client.logger.Debug("client unregistered", "clients", len(h.clients))
}

func (h *Hub) forgetUserConnection(client *Client) {
	conns := h.users[client.userID]
	delete(conns, client)
	if len(conns) == 0 {
		delete(h.users, client.userID)
	}
}

func (h *Hub) userHasConnectionInRoom(userID, roomID string) bool {
	for conn := range h.users[userID] {
		if _, joined := conn.rooms[roomID]; joined {
			return true
		}
	}
	return false
}

func (h *Hub) announcePresence(roomID, userID, status string) {
	frame, err := encodeTextFrame(newPresence(roomID, userID, status))
	if err != nil {
		h.logger.Error("encode presence", "error", err)
		return
	}
	h.broadcast(roomID, frame, userID)
}

func (h *Hub) broadcast(roomID string, frame outboundFrame, excludedUserID string) {
	for member := range h.rooms[roomID] {
		if member.userID != excludedUserID {
			h.deliver(member, frame)
		}
	}
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
	client.stop(websocket.ClosePolicyViolation, "send buffer full", reasonSlowClient)
}
