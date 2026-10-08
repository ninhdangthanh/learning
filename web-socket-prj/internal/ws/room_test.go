package ws

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const (
	replyTimeout     = 2 * time.Second
	silenceWindow    = 200 * time.Millisecond
	statsPollTimeout = 2 * time.Second
)

type serverMessage struct {
	Type        string `json:"type"`
	RoomID      string `json:"room_id"`
	SenderID    string `json:"sender_id"`
	Content     string `json:"content"`
	Code        string `json:"code"`
	RequestType string `json:"request_type"`
}

type testServer struct {
	hub   *Hub
	wsURL string
}

func startTestServer(t *testing.T) testServer {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	hub := NewHub(logger)
	go hub.Run(ctx)

	server := httptest.NewServer(NewHandler(DefaultConfig(), hub, logger))
	t.Cleanup(func() {
		server.Close()
		cancel()
	})
	return testServer{hub: hub, wsURL: "ws" + strings.TrimPrefix(server.URL, "http")}
}

func (s testServer) dial(t *testing.T) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(s.wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func (s testServer) waitForStats(t *testing.T, want Stats) {
	t.Helper()
	deadline := time.Now().Add(statsPollTimeout)
	for {
		got := s.hub.Stats()
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("stats = %+v, want %+v", got, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func sendJSON(t *testing.T, conn *websocket.Conn, value any) {
	t.Helper()
	if err := conn.WriteJSON(value); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func readMessage(t *testing.T, conn *websocket.Conn) serverMessage {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(replyTimeout)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	var message serverMessage
	if err := conn.ReadJSON(&message); err != nil {
		t.Fatalf("read: %v", err)
	}
	return message
}

func expectNoMessage(t *testing.T, conn *websocket.Conn, window time.Duration) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(window)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	_, payload, err := conn.ReadMessage()
	if err == nil {
		t.Fatalf("unexpected message: %s", payload)
	}
	if !isTimeout(err) {
		t.Fatalf("expected read timeout, got %v", err)
	}
}

func expectError(t *testing.T, conn *websocket.Conn, wantCode string) serverMessage {
	t.Helper()
	message := readMessage(t, conn)
	if message.Type != messageTypeError || message.Code != wantCode {
		t.Fatalf("got %+v, want error %s", message, wantCode)
	}
	return message
}

func joinRoom(t *testing.T, conn *websocket.Conn, roomID string) {
	t.Helper()
	sendJSON(t, conn, inboundEnvelope{Type: messageTypeJoinRoom, RoomID: roomID})
	ack := readMessage(t, conn)
	if ack.Type != messageTypeJoined || ack.RoomID != roomID {
		t.Fatalf("got %+v, want joined %s", ack, roomID)
	}
}

func leaveRoom(t *testing.T, conn *websocket.Conn, roomID string) {
	t.Helper()
	sendJSON(t, conn, inboundEnvelope{Type: messageTypeLeaveRoom, RoomID: roomID})
	ack := readMessage(t, conn)
	if ack.Type != messageTypeLeft || ack.RoomID != roomID {
		t.Fatalf("got %+v, want left %s", ack, roomID)
	}
}

func sendChat(t *testing.T, conn *websocket.Conn, roomID, content string) {
	t.Helper()
	sendJSON(t, conn, inboundEnvelope{Type: messageTypeChat, RoomID: roomID, Content: content})
}

func expectChat(t *testing.T, conn *websocket.Conn, roomID, content string) {
	t.Helper()
	message := readMessage(t, conn)
	if message.Type != messageTypeChat || message.RoomID != roomID || message.Content != content {
		t.Fatalf("got %+v, want message %q in %s", message, content, roomID)
	}
}

func closeConnection(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	closeFrame := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
	_ = conn.WriteControl(websocket.CloseMessage, closeFrame, time.Now().Add(time.Second))
	_ = conn.Close()
}

func TestJoinRoomReturnsAck(t *testing.T) {
	server := startTestServer(t)
	conn := server.dial(t)

	joinRoom(t, conn, "room-123")
}

func TestJoinRoomIsIdempotent(t *testing.T) {
	server := startTestServer(t)
	conn := server.dial(t)

	joinRoom(t, conn, "room-123")
	joinRoom(t, conn, "room-123")
	server.waitForStats(t, Stats{Clients: 1, Rooms: 1})
}

func TestMessageOnlyReachesRoomMembers(t *testing.T) {
	server := startTestServer(t)
	alice, bob, carol := server.dial(t), server.dial(t), server.dial(t)
	joinRoom(t, alice, "room-123")
	joinRoom(t, alice, "room-456")
	joinRoom(t, bob, "room-123")
	joinRoom(t, carol, "room-456")

	sendChat(t, alice, "room-123", "hello 123")

	expectChat(t, alice, "room-123", "hello 123")
	expectChat(t, bob, "room-123", "hello 123")
	expectNoMessage(t, carol, silenceWindow)
}

func TestClientInMultipleRooms(t *testing.T) {
	server := startTestServer(t)
	alice, bob, carol := server.dial(t), server.dial(t), server.dial(t)
	joinRoom(t, alice, "room-123")
	joinRoom(t, alice, "room-456")
	joinRoom(t, bob, "room-123")
	joinRoom(t, carol, "room-456")

	sendChat(t, bob, "room-123", "from bob")
	expectChat(t, alice, "room-123", "from bob")
	expectChat(t, bob, "room-123", "from bob")

	sendChat(t, carol, "room-456", "from carol")
	expectChat(t, alice, "room-456", "from carol")
	expectChat(t, carol, "room-456", "from carol")

	expectNoMessage(t, bob, silenceWindow)
}

func TestLeaveRoomStopsDelivery(t *testing.T) {
	server := startTestServer(t)
	alice, bob := server.dial(t), server.dial(t)
	joinRoom(t, alice, "room-123")
	joinRoom(t, bob, "room-123")

	leaveRoom(t, bob, "room-123")
	sendChat(t, alice, "room-123", "after leave")

	expectChat(t, alice, "room-123", "after leave")
	expectNoMessage(t, bob, silenceWindow)
}

func TestSendToRoomNotJoined(t *testing.T) {
	server := startTestServer(t)
	conn := server.dial(t)

	sendChat(t, conn, "room-123", "blind send")
	message := expectError(t, conn, ErrorCodeNotInRoom)
	if message.RoomID != "room-123" || message.RequestType != messageTypeChat {
		t.Fatalf("error should echo request, got %+v", message)
	}
}

func TestLeaveRoomNotJoined(t *testing.T) {
	server := startTestServer(t)
	conn := server.dial(t)

	sendJSON(t, conn, inboundEnvelope{Type: messageTypeLeaveRoom, RoomID: "room-123"})
	expectError(t, conn, ErrorCodeNotInRoom)
}

func TestEmptyRoomIsCleanedUp(t *testing.T) {
	server := startTestServer(t)
	leaver, disconnector := server.dial(t), server.dial(t)

	joinRoom(t, leaver, "room-leave")
	joinRoom(t, disconnector, "room-disconnect")
	server.waitForStats(t, Stats{Clients: 2, Rooms: 2})

	leaveRoom(t, leaver, "room-leave")
	server.waitForStats(t, Stats{Clients: 2, Rooms: 1})

	closeConnection(t, disconnector)
	server.waitForStats(t, Stats{Clients: 1, Rooms: 0})
}

func TestDisconnectRemovesClientFromAllRooms(t *testing.T) {
	server := startTestServer(t)
	stayer, quitter := server.dial(t), server.dial(t)
	joinRoom(t, stayer, "room-shared")
	for _, roomID := range []string{"room-shared", "room-a", "room-b"} {
		joinRoom(t, quitter, roomID)
	}
	server.waitForStats(t, Stats{Clients: 2, Rooms: 3})

	closeConnection(t, quitter)
	server.waitForStats(t, Stats{Clients: 1, Rooms: 1})

	sendChat(t, stayer, "room-shared", "still here")
	expectChat(t, stayer, "room-shared", "still here")
}

func TestTooManyRooms(t *testing.T) {
	server := startTestServer(t)
	conn := server.dial(t)
	for i := range MaxRoomsPerClient {
		joinRoom(t, conn, "room-"+strings.Repeat("x", i+1))
	}

	sendJSON(t, conn, inboundEnvelope{Type: messageTypeJoinRoom, RoomID: "one-too-many"})
	expectError(t, conn, ErrorCodeTooManyRooms)
}

func TestProtocolErrorsKeepConnectionOpen(t *testing.T) {
	cases := []struct {
		name     string
		payload  string
		wantCode string
	}{
		{"InvalidRoomID", `{"type":"join_room","room_id":"Bad Room!"}`, ErrorCodeInvalidRoomID},
		{"MissingRoomID", `{"type":"join_room"}`, ErrorCodeMalformedMessage},
		{"UnknownType", `{"type":"dance","room_id":"room-123"}`, ErrorCodeUnknownType},
		{"MissingType", `{"room_id":"room-123"}`, ErrorCodeMalformedMessage},
		{"BrokenJSON", `{"type":`, ErrorCodeMalformedMessage},
		{"MissingContent", `{"type":"message","room_id":"room-123"}`, ErrorCodeMalformedMessage},
		{"ContentTooLong", mustMarshal(t, inboundEnvelope{Type: messageTypeChat, RoomID: "room-123", Content: strings.Repeat("é", MaxContentLength+1)}), ErrorCodeContentTooLong},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := startTestServer(t)
			conn := server.dial(t)

			if err := conn.WriteMessage(websocket.TextMessage, []byte(tc.payload)); err != nil {
				t.Fatalf("write: %v", err)
			}
			expectError(t, conn, tc.wantCode)

			joinRoom(t, conn, "room-123")
		})
	}
}

func TestContentAtLimitIsAccepted(t *testing.T) {
	server := startTestServer(t)
	conn := server.dial(t)
	joinRoom(t, conn, "room-123")

	content := strings.Repeat("ữ", MaxContentLength)
	sendChat(t, conn, "room-123", content)
	expectChat(t, conn, "room-123", content)
}

func TestJoinThenMessageOrderPreserved(t *testing.T) {
	server := startTestServer(t)
	conn := server.dial(t)

	sendJSON(t, conn, inboundEnvelope{Type: messageTypeJoinRoom, RoomID: "room-123"})
	sendChat(t, conn, "room-123", "right after join")

	ack := readMessage(t, conn)
	if ack.Type != messageTypeJoined {
		t.Fatalf("got %+v, want joined", ack)
	}
	expectChat(t, conn, "room-123", "right after join")
}

func TestDecodeInboundRoomIDPattern(t *testing.T) {
	cases := map[string]bool{
		"room-123":              true,
		"a":                     true,
		"0_room":                true,
		strings.Repeat("a", 64): true,
		strings.Repeat("a", 65): false,
		"":                      false,
		"-room":                 false,
		"Room":                  false,
		"room 1":                false,
		"phòng":                 false,
	}
	for roomID, wantValid := range cases {
		payload := mustMarshal(t, inboundEnvelope{Type: messageTypeJoinRoom, RoomID: roomID})
		_, protoErr := decodeInbound([]byte(payload))
		if gotValid := protoErr == nil; gotValid != wantValid {
			t.Errorf("room_id %q: valid = %v, want %v (err %+v)", roomID, gotValid, wantValid, protoErr)
		}
	}
}

func mustMarshal(t *testing.T, value any) string {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(payload)
}
