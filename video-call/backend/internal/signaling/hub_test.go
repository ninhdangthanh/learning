package signaling

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"video-call-backend/internal/auth"
	"video-call-backend/internal/database"
	"video-call-backend/internal/models"

	"github.com/gorilla/websocket"
)

const readTimeout = 2 * time.Second

type testEnv struct {
	db     *sql.DB
	server *httptest.Server
	auth   *auth.Handler
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	db, err := database.InitDB(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}

	hub := NewHub(db)
	go hub.Run()

	server := httptest.NewServer(http.HandlerFunc(hub.HandleWebSocket))
	t.Cleanup(func() {
		server.Close()
		_ = db.Close()
	})

	return &testEnv{db: db, server: server, auth: auth.NewHandler(db)}
}

func (e *testEnv) register(t *testing.T, username string) models.AuthResponse {
	t.Helper()
	body, _ := json.Marshal(models.RegisterRequest{
		Username:    username,
		Email:       username + "@example.com",
		Password:    "secret123",
		DisplayName: strings.ToUpper(username),
	})
	rec := httptest.NewRecorder()
	e.auth.Register(rec, httptest.NewRequest(http.MethodPost, "/api/auth/register", strings.NewReader(string(body))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("register %s: status %d", username, rec.Code)
	}
	var resp models.AuthResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode register: %v", err)
	}
	return resp
}

func (e *testEnv) wsURL(token string) string {
	return "ws" + strings.TrimPrefix(e.server.URL, "http") + "/ws?token=" + token
}

func (e *testEnv) dial(t *testing.T, token string) *websocket.Conn {
	t.Helper()
	conn, resp, err := websocket.DefaultDialer.Dial(e.wsURL(token), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = resp.Body.Close()
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func readMessage(t *testing.T, conn *websocket.Conn) SignalMessage {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	var msg SignalMessage
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read message: %v", err)
	}
	return msg
}

func readUntilType(t *testing.T, conn *websocket.Conn, msgType string) SignalMessage {
	t.Helper()
	for {
		msg := readMessage(t, conn)
		if msg.Type == msgType {
			return msg
		}
	}
}

func onlineUserIDs(t *testing.T, msg SignalMessage) map[int64]bool {
	t.Helper()
	var users []struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(msg.Payload, &users); err != nil {
		t.Fatalf("decode users-list payload: %v", err)
	}
	ids := make(map[int64]bool, len(users))
	for _, u := range users {
		ids[u.ID] = true
	}
	return ids
}

func waitForOnlineUsers(t *testing.T, conn *websocket.Conn, want int) map[int64]bool {
	t.Helper()
	for {
		ids := onlineUserIDs(t, readUntilType(t, conn, "users-list"))
		if len(ids) == want {
			return ids
		}
	}
}

func TestHandleWebSocketRejectsMissingOrInvalidToken(t *testing.T) {
	env := newTestEnv(t)

	tests := []struct {
		name  string
		token string
	}{
		{"missing token", ""},
		{"invalid token", "not.a.jwt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, resp, err := websocket.DefaultDialer.Dial(env.wsURL(tt.token), nil)
			if err == nil {
				t.Fatal("expected handshake to fail")
			}
			if resp == nil {
				t.Fatalf("expected HTTP response, got error %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
			}
		})
	}
}

func TestPresenceBroadcastOnConnectAndDisconnect(t *testing.T) {
	env := newTestEnv(t)
	alice := env.register(t, "alice")
	bob := env.register(t, "bob")

	aliceConn := env.dial(t, alice.Token)
	waitForOnlineUsers(t, aliceConn, 1)

	bobConn := env.dial(t, bob.Token)
	ids := waitForOnlineUsers(t, aliceConn, 2)
	if !ids[alice.User.ID] || !ids[bob.User.ID] {
		t.Fatalf("online users = %v, want alice and bob", ids)
	}

	_ = bobConn.Close()
	ids = waitForOnlineUsers(t, aliceConn, 1)
	if ids[bob.User.ID] {
		t.Errorf("bob should be offline after disconnect, got %v", ids)
	}
}

func TestRouteMessageDeliversToTargetAndOverridesSender(t *testing.T) {
	env := newTestEnv(t)
	alice := env.register(t, "alice")
	bob := env.register(t, "bob")

	aliceConn := env.dial(t, alice.Token)
	bobConn := env.dial(t, bob.Token)
	waitForOnlineUsers(t, aliceConn, 2)
	waitForOnlineUsers(t, bobConn, 2)

	spoofed := SignalMessage{
		Type:     "offer",
		From:     424242,
		FromName: "mallory",
		To:       bob.User.ID,
		Payload:  json.RawMessage(`{"sdp":"v=0"}`),
	}
	if err := aliceConn.WriteJSON(spoofed); err != nil {
		t.Fatalf("write: %v", err)
	}

	got := readUntilType(t, bobConn, "offer")
	if got.From != alice.User.ID {
		t.Errorf("From = %d, want server-assigned %d (client must not spoof sender)", got.From, alice.User.ID)
	}
	if got.FromName != "ALICE" {
		t.Errorf("FromName = %q, want display name from DB %q", got.FromName, "ALICE")
	}
	if string(got.Payload) != `{"sdp":"v=0"}` {
		t.Errorf("payload = %s, want it forwarded untouched", got.Payload)
	}
}

func TestRouteMessageIgnoresUnknownTypes(t *testing.T) {
	env := newTestEnv(t)
	alice := env.register(t, "alice")
	bob := env.register(t, "bob")

	aliceConn := env.dial(t, alice.Token)
	bobConn := env.dial(t, bob.Token)
	waitForOnlineUsers(t, aliceConn, 2)
	waitForOnlineUsers(t, bobConn, 2)

	if err := aliceConn.WriteJSON(SignalMessage{Type: "drop-table", To: bob.User.ID}); err != nil {
		t.Fatalf("write unknown: %v", err)
	}
	if err := aliceConn.WriteJSON(SignalMessage{Type: "call", To: bob.User.ID}); err != nil {
		t.Fatalf("write call: %v", err)
	}

	if got := readMessage(t, bobConn); got.Type != "call" {
		t.Errorf("first message = %q, want unknown type to be dropped and %q delivered", got.Type, "call")
	}
}

func TestIsUserOnline(t *testing.T) {
	hub := NewHub(nil)
	hub.clients[7] = &Client{UserID: 7}

	if !hub.IsUserOnline(7) {
		t.Error("user 7 should be online")
	}
	if hub.IsUserOnline(8) {
		t.Error("user 8 should be offline")
	}
}
