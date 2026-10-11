package ws

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
	"github.com/ninhdang/ws-chat/internal/auth"
)

const CloseTokenExpired = 4001

type outboundFrame struct {
	messageType int
	payload     []byte
}

type Client struct {
	id          uint64
	ctx         context.Context
	userID      string
	expiresAt   time.Time
	hub         *Hub
	memberships MembershipChecker
	conn        *websocket.Conn
	cfg         Config
	logger      *slog.Logger
	send        chan outboundFrame
	done        chan struct{}
	readerDone  chan struct{}
	writerDone  chan struct{}
	stopOnce    sync.Once
	closeCode   int
	closeReason string
	rooms       map[string]struct{}
}

type clientDeps struct {
	hub         *Hub
	memberships MembershipChecker
	cfg         Config
	logger      *slog.Logger
}

func newClient(ctx context.Context, id uint64, conn *websocket.Conn, identity auth.Identity, deps clientDeps) *Client {
	return &Client{
		id:          id,
		ctx:         ctx,
		userID:      identity.UserID,
		expiresAt:   identity.ExpiresAt,
		hub:         deps.hub,
		memberships: deps.memberships,
		conn:        conn,
		cfg:         deps.cfg,
		logger:      deps.logger.With("conn_id", id, "user_id", identity.UserID, "remote_addr", conn.RemoteAddr().String()),
		send:        make(chan outboundFrame, deps.cfg.SendBufferSize),
		done:        make(chan struct{}),
		readerDone:  make(chan struct{}),
		writerDone:  make(chan struct{}),
		rooms:       make(map[string]struct{}),
	}
}

func (c *Client) run() {
	go c.writePump()
	c.readPump()
	<-c.writerDone
}

func (c *Client) stop(code int, reason string) {
	c.stopOnce.Do(func() {
		c.closeCode = code
		c.closeReason = reason
		close(c.done)
	})
}

func (c *Client) isStopped() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

func (c *Client) trySend(frame outboundFrame) bool {
	if c.isStopped() {
		return false
	}
	select {
	case c.send <- frame:
		return true
	default:
		return false
	}
}

func (c *Client) readPump() {
	defer func() {
		c.hub.Unregister(c)
		c.stop(websocket.CloseNormalClosure, "")
		close(c.readerDone)
	}()

	c.conn.SetReadLimit(c.cfg.MaxMessageSize)
	c.extendReadDeadline()
	c.conn.SetPongHandler(func(string) error {
		c.logger.Debug("pong received")
		return c.extendReadDeadline()
	})

	for {
		messageType, payload, err := c.conn.ReadMessage()
		if err != nil {
			c.logReadError(err)
			return
		}
		if c.isStopped() {
			continue
		}
		c.handleFrame(messageType, payload)
	}
}

func (c *Client) extendReadDeadline() error {
	return c.conn.SetReadDeadline(time.Now().Add(c.cfg.PongWait))
}

func (c *Client) handleFrame(messageType int, payload []byte) {
	switch messageType {
	case websocket.TextMessage:
		c.handleText(payload)
	case websocket.BinaryMessage:
		c.logger.Debug("binary frame rejected", "bytes", len(payload))
		c.reply(newErrorMessage(ErrorCodeUnsupportedMessageType, "binary frames are not supported"))
	}
}

func (c *Client) handleText(payload []byte) {
	if !utf8.Valid(payload) {
		c.closeWith(websocket.CloseInvalidFramePayloadData, "text frame is not valid UTF-8")
		return
	}

	envelope, protoErr := decodeInbound(payload)
	if protoErr != nil {
		c.logger.Info("inbound message rejected", "code", protoErr.Code, "error", protoErr.Message)
		c.reply(protoErr)
		return
	}

	c.logger.Debug("text frame received", "type", envelope.Type, "room_id", envelope.RoomID)
	switch envelope.Type {
	case messageTypeJoinRoom:
		c.join(envelope)
	case messageTypeLeaveRoom:
		c.hub.Leave(c, envelope.RoomID)
	case messageTypeChat:
		c.publish(envelope.RoomID, envelope.Content)
	}
}

func (c *Client) join(request inboundEnvelope) {
	allowed, err := c.isMember(request.RoomID)
	if err != nil {
		c.logger.Error("membership check failed, denying join", "room_id", request.RoomID, "error", err)
		c.reply(newRequestError(ErrorCodeInternal, "cannot verify room membership", request))
		return
	}
	if !allowed {
		c.logger.Info("join forbidden", "room_id", request.RoomID)
		c.reply(newRequestError(ErrorCodeForbidden, "You cannot join this room", request))
		return
	}
	c.hub.Join(c, request.RoomID)
}

func (c *Client) isMember(roomID string) (bool, error) {
	ctx, cancel := context.WithTimeout(c.ctx, c.cfg.MembershipWait)
	defer cancel()
	return c.memberships.IsMember(ctx, c.userID, roomID)
}

func (c *Client) publish(roomID, content string) {
	payload, ok := c.encode(newOutboundMessage(roomID, c.userID, content, time.Now().UTC()))
	if !ok {
		return
	}
	c.hub.Publish(c, roomID, payload)
}

func (c *Client) reply(value any) {
	payload, ok := c.encode(value)
	if !ok {
		return
	}
	if !c.trySend(outboundFrame{messageType: websocket.TextMessage, payload: payload}) {
		c.closeWith(websocket.ClosePolicyViolation, "send buffer full")
	}
}

func (c *Client) encode(value any) ([]byte, bool) {
	payload, err := json.Marshal(value)
	if err != nil {
		c.logger.Error("encode outbound message", "error", err)
		c.closeWith(websocket.CloseInternalServerErr, ErrorCodeInternal)
		return nil, false
	}
	return payload, true
}

func (c *Client) closeWith(code int, reason string) {
	c.logger.Info("closing connection", "code", code, "reason", reason)
	c.stop(code, reason)
}

func (c *Client) logReadError(err error) {
	switch {
	case websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway):
		c.logger.Info("client closed connection", "error", err)
	case c.isStopped():
		c.logger.Info("close handshake finished", "error", err)
	case errors.Is(err, websocket.ErrReadLimit):
		c.logger.Warn("message exceeds read limit", "limit", c.cfg.MaxMessageSize)
	case isTimeout(err):
		c.logger.Warn("read deadline exceeded, peer considered dead", "error", err)
	default:
		c.logger.Warn("unexpected disconnect", "error", err)
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(c.cfg.PingPeriod)
	tokenExpiry := time.NewTimer(time.Until(c.expiresAt))
	defer func() {
		ticker.Stop()
		tokenExpiry.Stop()
		_ = c.conn.Close()
		close(c.writerDone)
	}()

	for {
		select {
		case frame := <-c.send:
			if err := c.write(frame.messageType, frame.payload); err != nil {
				c.abortOnWriteError(err)
				return
			}
		case <-ticker.C:
			if err := c.write(websocket.PingMessage, nil); err != nil {
				c.abortOnWriteError(err)
				return
			}
			c.logger.Debug("ping sent")
		case <-tokenExpiry.C:
			c.closeWith(CloseTokenExpired, "token expired")
		case <-c.done:
			c.sendCloseFrame(c.closeCode, c.closeReason)
			c.awaitReader()
			return
		}
	}
}

func (c *Client) write(messageType int, payload []byte) error {
	if err := c.conn.SetWriteDeadline(time.Now().Add(c.cfg.WriteWait)); err != nil {
		return err
	}
	return c.conn.WriteMessage(messageType, payload)
}

func (c *Client) sendCloseFrame(code int, reason string) {
	err := c.write(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason))
	if err != nil && !errors.Is(err, websocket.ErrCloseSent) {
		c.logger.Debug("send close frame", "error", err)
	}
}

func (c *Client) awaitReader() {
	timer := time.NewTimer(c.cfg.WriteWait)
	defer timer.Stop()
	select {
	case <-c.readerDone:
	case <-timer.C:
	}
}

func (c *Client) abortOnWriteError(err error) {
	if !errors.Is(err, websocket.ErrCloseSent) {
		c.logger.Warn("write failed", "error", err)
	}
	c.stop(websocket.CloseAbnormalClosure, "write failed")
}

func isTimeout(err error) bool {
	var timeoutErr interface{ Timeout() bool }
	return errors.As(err, &timeoutErr) && timeoutErr.Timeout()
}
