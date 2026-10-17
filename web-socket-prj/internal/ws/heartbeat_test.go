package ws

import (
	"context"
	"expvar"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ninhdang/ws-chat/internal/ws/testutil"
)

const (
	fastPingPeriod = 100 * time.Millisecond
	fastPongWait   = 400 * time.Millisecond
	fastWriteWait  = 200 * time.Millisecond
	metricTimeout  = 5 * time.Second
)

func fastHeartbeat(cfg *Config) {
	cfg.PingPeriod = fastPingPeriod
	cfg.PongWait = fastPongWait
	cfg.WriteWait = fastWriteWait
}

func (m *Metrics) disconnectCount(reason disconnectReason) int64 {
	counter, ok := m.Disconnects.Get(string(reason)).(*expvar.Int)
	if !ok {
		return 0
	}
	return counter.Value()
}

func waitForMetric(t *testing.T, name string, read func() int64, want int64) {
	t.Helper()
	deadline := time.Now().Add(metricTimeout)
	for {
		got := read()
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s = %d, want %d", name, got, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (s testServer) dialBlackholed(t *testing.T, userID string) (*websocket.Conn, *testutil.BlackholeConn) {
	t.Helper()
	var netConn *testutil.BlackholeConn
	dialer := websocket.Dialer{
		NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := testutil.DialBlackhole(ctx, network, addr)
			netConn = conn
			return conn, err
		},
	}
	conn, resp, err := dialer.Dial(s.wsURL, bearerHeader(s.issueToken(t, userID, testTokenTTL)))
	if err != nil {
		t.Fatalf("dial: %v (status %d)", err, statusOf(resp))
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn, netConn
}

func TestConfigValidate(t *testing.T) {
	cases := map[string]func(*Config){
		"PingEqualsPongWait": func(c *Config) { c.PingPeriod = c.PongWait },
		"PingLongerThanPong": func(c *Config) { c.PingPeriod = c.PongWait + time.Second },
		"ZeroWriteWait":      func(c *Config) { c.WriteWait = 0 },
		"NegativePongWait":   func(c *Config) { c.PongWait = -time.Second },
		"ZeroPingPeriod":     func(c *Config) { c.PingPeriod = 0 },
		"ZeroSendBuffer":     func(c *Config) { c.SendBufferSize = 0 },
		"ZeroMaxMessageSize": func(c *Config) { c.MaxMessageSize = 0 },
		"ZeroMembershipWait": func(c *Config) { c.MembershipWait = 0 },
	}
	for name, breakConfig := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := DefaultConfig()
			breakConfig(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatalf("Validate accepted %+v", cfg)
			}
		})
	}

	if err := DefaultConfig().Validate(); err != nil {
		t.Fatalf("default config rejected: %v", err)
	}
}

func TestAppPingReturnsPong(t *testing.T) {
	server := startTestServer(t)
	conn := server.dial(t)

	sendJSON(t, conn, map[string]string{"type": messageTypePing})

	if message := readMessage(t, conn); message.Type != messageTypePong {
		t.Fatalf("got %+v, want pong", message)
	}
}

func TestBlackholedClientDetectedWithinPongWait(t *testing.T) {
	server := startTestServerWith(t, testServerOptions{configure: fastHeartbeat})
	conn, netConn := server.dialBlackholed(t, "user-a")
	joinRoom(t, conn, "room-123")

	netConn.Blackhole()
	holedAt := time.Now()

	waitForMetric(t, "pong_timeout disconnects", func() int64 { return server.metrics.disconnectCount(reasonPongTimeout) }, 1)
	if elapsed := time.Since(holedAt); elapsed > fastPongWait+300*time.Millisecond {
		t.Fatalf("dead connection detected after %v, want within pong wait %v (+ε)", elapsed, fastPongWait)
	}
	if dead := server.metrics.DeadConnections.Value(); dead != 1 {
		t.Fatalf("ws_dead_connections_total = %d, want 1", dead)
	}
	server.waitForStats(t, Stats{})
}

func TestActiveClientNeverTimesOut(t *testing.T) {
	server := startTestServerWith(t, testServerOptions{configure: fastHeartbeat})
	conn := server.dial(t)
	conn.SetPingHandler(func(string) error { return nil })

	keepAliveFor := 4 * fastPongWait
	for started := time.Now(); time.Since(started) < keepAliveFor; {
		sendJSON(t, conn, map[string]string{"type": messageTypePing})
		if message := readMessage(t, conn); message.Type != messageTypePong {
			t.Fatalf("got %+v, want pong", message)
		}
		time.Sleep(fastPingPeriod)
	}

	if timeouts := server.metrics.disconnectCount(reasonPongTimeout); timeouts != 0 {
		t.Fatalf("pong_timeout disconnects = %d, want 0", timeouts)
	}
	server.waitForStats(t, Stats{Clients: 1, Users: 1})
}

func TestWriteDeadlineClosesStuckWriter(t *testing.T) {
	server := startTestServerWith(t, testServerOptions{configure: func(cfg *Config) {
		cfg.WriteWait = fastWriteWait
		cfg.SendBufferSize = 4096
	}})
	stuck, flooder := server.dialAs(t, "stuck"), server.dialAs(t, "flooder")
	joinRoom(t, stuck, "room-123")
	joinRoom(t, flooder, "room-123")
	go drain(flooder)

	content := strings.Repeat("x", MaxContentLength)
	deadline := time.Now().Add(metricTimeout)
	for server.metrics.disconnectCount(reasonWriteFailed) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("stuck writer was never closed by the write deadline")
		}
		for range 50 {
			sendChat(t, flooder, "room-123", content)
		}
	}

	server.waitForStats(t, Stats{Clients: 1, Users: 1, Rooms: 1})
	if perUser := server.hub.Stats().ConnectionsPerUser; perUser["stuck"] != 0 || perUser["flooder"] != 1 {
		t.Fatalf("connections_per_user = %v, want only flooder", perUser)
	}
}

func TestMetricsCountDisconnectReasons(t *testing.T) {
	server := startTestServer(t)

	polite := server.dial(t)
	sendJSON(t, polite, map[string]string{"type": messageTypePing})
	readMessage(t, polite)
	closeConnection(t, polite)
	waitForMetric(t, "client_closed disconnects", func() int64 { return server.metrics.disconnectCount(reasonClientClosed) }, 1)

	abrupt := server.dial(t)
	_ = abrupt.Close()
	waitForMetric(t, "connection_lost disconnects", func() int64 { return server.metrics.disconnectCount(reasonConnectionLost) }, 1)

	expiring := server.dialWithToken(t, server.issueToken(t, "user-expiring", time.Second))
	go drain(expiring)
	waitForMetric(t, "token_expired disconnects", func() int64 { return server.metrics.disconnectCount(reasonTokenExpired) }, 1)

	waitForMetric(t, "ws_active_connections", server.metrics.ActiveConnections.Value, 0)
	if in, out := server.metrics.MessagesIn.Value(), server.metrics.MessagesOut.Value(); in != 1 || out != 1 {
		t.Fatalf("messages in/out = %d/%d, want 1/1", in, out)
	}
}
