package ws

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const testTimeout = 2 * time.Second

func testConfig() Config {
	return Config{
		WriteWait:      time.Second,
		PongWait:       time.Second,
		PingPeriod:     900 * time.Millisecond,
		MaxMessageSize: 128,
		SendBufferSize: 4,
	}
}

func startServer(t *testing.T, cfg Config) (*Handler, string) {
	t.Helper()
	handler := NewHandler(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return handler, "ws" + strings.TrimPrefix(server.URL, "http")
}

func dial(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101", resp.StatusCode)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func readFrame(t *testing.T, conn *websocket.Conn) (int, []byte) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(testTimeout))
	messageType, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return messageType, payload
}

func expectCloseCode(t *testing.T, conn *websocket.Conn, wantCode int) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(testTimeout))
	for {
		_, _, err := conn.ReadMessage()
		if err == nil {
			continue
		}
		var closeErr *websocket.CloseError
		if !errors.As(err, &closeErr) {
			t.Fatalf("read error = %v, want close error %d", err, wantCode)
		}
		if closeErr.Code != wantCode {
			t.Fatalf("close code = %d, want %d", closeErr.Code, wantCode)
		}
		return
	}
}

func waitForNoActiveConnections(t *testing.T, handler *Handler) {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for handler.ActiveConnections() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("active connections = %d, want 0", handler.ActiveConnections())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestUpgradeRejectsPlainHTTPRequest(t *testing.T) {
	handler := NewHandler(testConfig(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	server := httptest.NewServer(handler)
	defer server.Close()

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestEchoesTextMessage(t *testing.T) {
	_, url := startServer(t, testConfig())
	conn := dial(t, url)

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"message":"hello"}`)); err != nil {
		t.Fatalf("write: %v", err)
	}

	messageType, payload := readFrame(t, conn)
	if messageType != websocket.TextMessage {
		t.Fatalf("message type = %d, want text", messageType)
	}
	var got EchoMessage
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Message != "hello" {
		t.Fatalf("message = %q, want hello", got.Message)
	}
}

func TestEchoesBinaryMessage(t *testing.T) {
	_, url := startServer(t, testConfig())
	conn := dial(t, url)
	sent := []byte{0x00, 0xff, 0x10, 0x80}

	if err := conn.WriteMessage(websocket.BinaryMessage, sent); err != nil {
		t.Fatalf("write: %v", err)
	}

	messageType, payload := readFrame(t, conn)
	if messageType != websocket.BinaryMessage {
		t.Fatalf("message type = %d, want binary", messageType)
	}
	if string(payload) != string(sent) {
		t.Fatalf("payload = %v, want %v", payload, sent)
	}
}

func TestMalformedMessageReturnsErrorAndKeepsConnectionOpen(t *testing.T) {
	cases := map[string]string{
		"invalid json":          `{"message":`,
		"missing message field": `{"text":"hello"}`,
		"wrong field type":      `{"message":123}`,
	}

	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			_, url := startServer(t, testConfig())
			conn := dial(t, url)

			if err := conn.WriteMessage(websocket.TextMessage, []byte(input)); err != nil {
				t.Fatalf("write: %v", err)
			}

			_, payload := readFrame(t, conn)
			var got ErrorMessage
			if err := json.Unmarshal(payload, &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.Type != "error" || got.Code != ErrorCodeMalformedMessage {
				t.Fatalf("got %+v, want error %s", got, ErrorCodeMalformedMessage)
			}

			if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"message":"still alive"}`)); err != nil {
				t.Fatalf("write after error: %v", err)
			}
			_, payload = readFrame(t, conn)
			if !strings.Contains(string(payload), "still alive") {
				t.Fatalf("payload = %s, want echo after error", payload)
			}
		})
	}
}

func TestInvalidUTF8TextClosesWithInvalidPayloadCode(t *testing.T) {
	handler, url := startServer(t, testConfig())
	conn := dial(t, url)

	if err := conn.WriteMessage(websocket.TextMessage, []byte{0xff, 0xfe}); err != nil {
		t.Fatalf("write: %v", err)
	}

	expectCloseCode(t, conn, websocket.CloseInvalidFramePayloadData)
	waitForNoActiveConnections(t, handler)
}

func TestOversizedMessageClosesWithMessageTooBigCode(t *testing.T) {
	handler, url := startServer(t, testConfig())
	conn := dial(t, url)

	if err := conn.WriteMessage(websocket.BinaryMessage, make([]byte, 1024)); err != nil {
		t.Fatalf("write: %v", err)
	}

	expectCloseCode(t, conn, websocket.CloseMessageTooBig)
	waitForNoActiveConnections(t, handler)
}

func TestServerAnswersClientPing(t *testing.T) {
	_, url := startServer(t, testConfig())
	conn := dial(t, url)

	pong := make(chan string, 1)
	conn.SetPongHandler(func(appData string) error {
		pong <- appData
		return nil
	})

	if err := conn.WriteControl(websocket.PingMessage, []byte("are-you-there"), time.Now().Add(time.Second)); err != nil {
		t.Fatalf("ping: %v", err)
	}
	go func() { _, _, _ = conn.ReadMessage() }()

	select {
	case got := <-pong:
		if got != "are-you-there" {
			t.Fatalf("pong payload = %q", got)
		}
	case <-time.After(testTimeout):
		t.Fatal("no pong received")
	}
}

func TestServerSendsPingAndStaysAliveWhenClientPongs(t *testing.T) {
	cfg := testConfig()
	cfg.PongWait = 300 * time.Millisecond
	cfg.PingPeriod = 100 * time.Millisecond
	handler, url := startServer(t, cfg)
	conn := dial(t, url)

	pings := make(chan struct{}, 16)
	conn.SetPingHandler(func(appData string) error {
		pings <- struct{}{}
		return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(time.Second))
	})
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	time.Sleep(3 * cfg.PongWait)

	if len(pings) < 3 {
		t.Fatalf("received %d pings, want at least 3", len(pings))
	}
	if handler.ActiveConnections() != 1 {
		t.Fatalf("active connections = %d, want 1", handler.ActiveConnections())
	}
}

func TestDeadClientIsDisconnectedAfterPongTimeout(t *testing.T) {
	cfg := testConfig()
	cfg.PongWait = 200 * time.Millisecond
	cfg.PingPeriod = 100 * time.Millisecond
	handler, url := startServer(t, cfg)
	conn := dial(t, url)

	conn.SetPingHandler(func(string) error { return nil })
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	waitForNoActiveConnections(t, handler)
}

func TestClientCloseFrameCleansUpConnection(t *testing.T) {
	handler, url := startServer(t, testConfig())
	conn := dial(t, url)

	closeFrame := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "bye")
	if err := conn.WriteControl(websocket.CloseMessage, closeFrame, time.Now().Add(time.Second)); err != nil {
		t.Fatalf("close: %v", err)
	}

	expectCloseCode(t, conn, websocket.CloseNormalClosure)
	waitForNoActiveConnections(t, handler)
}

func TestAbruptTCPDisconnectCleansUpConnection(t *testing.T) {
	handler, url := startServer(t, testConfig())
	conn := dial(t, url)

	if err := conn.NetConn().Close(); err != nil {
		t.Fatalf("close tcp: %v", err)
	}

	waitForNoActiveConnections(t, handler)
}

func TestHandlesManyConcurrentConnections(t *testing.T) {
	handler, url := startServer(t, testConfig())
	const clients = 20

	errs := make(chan error, clients)
	for i := range clients {
		go func() {
			conn, _, err := websocket.DefaultDialer.Dial(url, nil)
			if err != nil {
				errs <- err
				return
			}
			defer conn.Close()

			want := strings.Repeat("x", i+1)
			if err := conn.WriteJSON(EchoMessage{Message: want}); err != nil {
				errs <- err
				return
			}
			_ = conn.SetReadDeadline(time.Now().Add(testTimeout))
			var got EchoMessage
			if err := conn.ReadJSON(&got); err != nil {
				errs <- err
				return
			}
			if got.Message != want {
				errs <- errors.New("echo mismatch: " + got.Message)
				return
			}
			errs <- nil
		}()
	}

	for range clients {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	waitForNoActiveConnections(t, handler)
}

func dialWithOrigin(url, origin string) (*websocket.Conn, *http.Response, error) {
	header := http.Header{}
	if origin != "" {
		header.Set("Origin", origin)
	}
	return websocket.DefaultDialer.Dial(url, header)
}

func TestOriginPolicy(t *testing.T) {
	const extensionOrigin = "chrome-extension://abcdefghijklmnop"

	tests := []struct {
		name           string
		allowedOrigins []string
		origin         func(serverURL string) string
		wantAccepted   bool
	}{
		{"no origin header", nil, func(string) string { return "" }, true},
		{"same origin", nil, func(serverURL string) string { return serverURL }, true},
		{"foreign origin not allowed", nil, func(string) string { return extensionOrigin }, false},
		{"foreign origin in allowlist", []string{extensionOrigin}, func(string) string { return extensionOrigin }, true},
		{"wildcard allows any origin", []string{"*"}, func(string) string { return "http://evil.example" }, true},
		{"other origin not in allowlist", []string{extensionOrigin}, func(string) string { return "http://evil.example" }, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testConfig()
			cfg.AllowedOrigins = tt.allowedOrigins
			handler := NewHandler(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
			server := httptest.NewServer(handler)
			defer server.Close()
			url := "ws" + strings.TrimPrefix(server.URL, "http")

			conn, resp, err := dialWithOrigin(url, tt.origin(server.URL))
			if conn != nil {
				defer conn.Close()
			}

			if tt.wantAccepted {
				if err != nil {
					t.Fatalf("dial: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("dial succeeded, want rejection")
			}
			if resp == nil || resp.StatusCode != http.StatusForbidden {
				t.Fatalf("response = %v, want 403", resp)
			}
		})
	}
}
