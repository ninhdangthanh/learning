package ws

import (
	"encoding/json"
	"errors"
	"log/slog"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
)

type outboundFrame struct {
	messageType int
	payload     []byte
}

type connection struct {
	id         uint64
	conn       *websocket.Conn
	cfg        Config
	logger     *slog.Logger
	send       chan outboundFrame
	writerDone chan struct{}
	closing    bool
}

func newConnection(id uint64, conn *websocket.Conn, cfg Config, logger *slog.Logger) *connection {
	return &connection{
		id:         id,
		conn:       conn,
		cfg:        cfg,
		logger:     logger.With("conn_id", id, "remote_addr", conn.RemoteAddr().String()),
		send:       make(chan outboundFrame, cfg.SendBufferSize),
		writerDone: make(chan struct{}),
	}
}

func (c *connection) run() {
	go c.writePump()
	c.readPump()
	<-c.writerDone
}

func (c *connection) readPump() {
	defer close(c.send)

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
		if c.closing {
			continue
		}
		c.handleFrame(messageType, payload)
	}
}

func (c *connection) extendReadDeadline() error {
	return c.conn.SetReadDeadline(time.Now().Add(c.cfg.PongWait))
}

func (c *connection) handleFrame(messageType int, payload []byte) {
	switch messageType {
	case websocket.TextMessage:
		c.handleText(payload)
	case websocket.BinaryMessage:
		c.logger.Debug("binary frame received", "bytes", len(payload))
		c.enqueue(outboundFrame{messageType: websocket.BinaryMessage, payload: payload})
	}
}

func (c *connection) handleText(payload []byte) {
	if !utf8.Valid(payload) {
		c.initiateClose(websocket.CloseInvalidFramePayloadData, "text frame is not valid UTF-8")
		return
	}

	message, err := decodeEchoMessage(payload)
	if err != nil {
		c.logger.Info("malformed message", "error", err)
		c.enqueueJSON(newErrorMessage(ErrorCodeMalformedMessage, err.Error()))
		return
	}

	c.logger.Debug("text frame received", "message", message.Message)
	c.enqueueJSON(message)
}

func (c *connection) enqueueJSON(value any) {
	payload, err := json.Marshal(value)
	if err != nil {
		c.logger.Error("encode outbound message", "error", err)
		c.initiateClose(websocket.CloseInternalServerErr, ErrorCodeInternal)
		return
	}
	c.enqueue(outboundFrame{messageType: websocket.TextMessage, payload: payload})
}

func (c *connection) enqueue(frame outboundFrame) {
	select {
	case c.send <- frame:
	case <-c.writerDone:
	}
}

func (c *connection) initiateClose(code int, reason string) {
	c.logger.Info("closing connection", "code", code, "reason", reason)
	c.closing = true
	c.enqueue(outboundFrame{
		messageType: websocket.CloseMessage,
		payload:     websocket.FormatCloseMessage(code, reason),
	})
	_ = c.conn.SetReadDeadline(time.Now().Add(c.cfg.WriteWait))
}

func (c *connection) logReadError(err error) {
	switch {
	case websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway):
		c.logger.Info("client closed connection", "error", err)
	case c.closing:
		c.logger.Info("close handshake finished", "error", err)
	case errors.Is(err, websocket.ErrReadLimit):
		c.logger.Warn("message exceeds read limit", "limit", c.cfg.MaxMessageSize)
	case isTimeout(err):
		c.logger.Warn("read deadline exceeded, peer considered dead", "error", err)
	default:
		c.logger.Warn("unexpected disconnect", "error", err)
	}
}

func (c *connection) writePump() {
	ticker := time.NewTicker(c.cfg.PingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
		close(c.writerDone)
	}()

	for {
		select {
		case frame, ok := <-c.send:
			if !ok {
				c.sendCloseFrame(websocket.CloseNormalClosure, "")
				return
			}
			if err := c.write(frame.messageType, frame.payload); err != nil {
				c.logWriteError(err)
				return
			}
		case <-ticker.C:
			if err := c.write(websocket.PingMessage, nil); err != nil {
				c.logWriteError(err)
				return
			}
			c.logger.Debug("ping sent")
		}
	}
}

func (c *connection) write(messageType int, payload []byte) error {
	if err := c.conn.SetWriteDeadline(time.Now().Add(c.cfg.WriteWait)); err != nil {
		return err
	}
	return c.conn.WriteMessage(messageType, payload)
}

func (c *connection) sendCloseFrame(code int, reason string) {
	err := c.write(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason))
	if err != nil && !errors.Is(err, websocket.ErrCloseSent) {
		c.logger.Debug("send close frame", "error", err)
	}
}

func (c *connection) logWriteError(err error) {
	if errors.Is(err, websocket.ErrCloseSent) {
		return
	}
	c.logger.Warn("write failed", "error", err)
}

func isTimeout(err error) bool {
	var timeoutErr interface{ Timeout() bool }
	return errors.As(err, &timeoutErr) && timeoutErr.Timeout()
}
