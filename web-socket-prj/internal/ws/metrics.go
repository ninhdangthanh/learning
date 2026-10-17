package ws

import "expvar"

type disconnectReason string

const (
	reasonClientClosed       disconnectReason = "client_closed"
	reasonConnectionLost     disconnectReason = "connection_lost"
	reasonPongTimeout        disconnectReason = "pong_timeout"
	reasonWriteFailed        disconnectReason = "write_failed"
	reasonSlowClient         disconnectReason = "slow_client"
	reasonProtocolError      disconnectReason = "protocol_error"
	reasonInternalError      disconnectReason = "internal_error"
	reasonTokenExpired       disconnectReason = "token_expired"
	reasonTooManyConnections disconnectReason = "too_many_connections"
	reasonServerShutdown     disconnectReason = "server_shutdown"
)

type Metrics struct {
	ActiveConnections expvar.Int
	DeadConnections   expvar.Int
	SlowClientKicks   expvar.Int
	MessagesIn        expvar.Int
	MessagesOut       expvar.Int
	Disconnects       expvar.Map
}

func NewMetrics() *Metrics {
	return &Metrics{}
}

func (m *Metrics) Publish() {
	expvar.Publish("ws_active_connections", &m.ActiveConnections)
	expvar.Publish("ws_dead_connections_total", &m.DeadConnections)
	expvar.Publish("ws_slow_client_kicks_total", &m.SlowClientKicks)
	expvar.Publish("ws_messages_in_total", &m.MessagesIn)
	expvar.Publish("ws_messages_out_total", &m.MessagesOut)
	expvar.Publish("ws_disconnects_total", &m.Disconnects)
}

func (m *Metrics) recordDisconnect(reason disconnectReason) {
	m.Disconnects.Add(string(reason), 1)
	switch reason {
	case reasonPongTimeout:
		m.DeadConnections.Add(1)
	case reasonSlowClient:
		m.SlowClientKicks.Add(1)
	}
}
