package ws

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func (h *Hub) checkInvariants() error {
	for client := range h.clients {
		if _, indexed := h.users[client.userID][client]; !indexed {
			return fmt.Errorf("client %d registered but missing from users[%s]", client.id, client.userID)
		}
		for roomID := range client.rooms {
			if _, member := h.rooms[roomID][client]; !member {
				return fmt.Errorf("client %d thinks it is in %s but room does not list it", client.id, roomID)
			}
		}
	}
	for userID, conns := range h.users {
		if len(conns) == 0 {
			return fmt.Errorf("users[%s] exists but is empty", userID)
		}
		for client := range conns {
			if _, registered := h.clients[client]; !registered || client.userID != userID {
				return fmt.Errorf("users[%s] holds stale client %d", userID, client.id)
			}
		}
	}
	for roomID, members := range h.rooms {
		if len(members) == 0 {
			return fmt.Errorf("rooms[%s] exists but is empty", roomID)
		}
		for client := range members {
			if _, registered := h.clients[client]; !registered {
				return fmt.Errorf("rooms[%s] holds unregistered client %d", roomID, client.id)
			}
			if _, joined := client.rooms[roomID]; !joined {
				return fmt.Errorf("rooms[%s] lists client %d which does not track the room", roomID, client.id)
			}
		}
	}
	return nil
}

func (s testServer) dialDevices(t *testing.T, userID string, count int) []*websocket.Conn {
	t.Helper()
	devices := make([]*websocket.Conn, count)
	for i := range devices {
		devices[i] = s.dialAs(t, userID)
	}
	return devices
}

func expectPresence(t *testing.T, conn *websocket.Conn, roomID, userID, status string) {
	t.Helper()
	message := readFrame(t, conn)
	if message.Type != messageTypePresence || message.RoomID != roomID || message.UserID != userID || message.Status != status {
		t.Fatalf("got %+v, want presence %s %s in %s", message, userID, status, roomID)
	}
}

func expectNextFrameIsChat(t *testing.T, conn *websocket.Conn, roomID, content string) {
	t.Helper()
	message := readFrame(t, conn)
	if message.Type != messageTypeChat || message.RoomID != roomID || message.Content != content {
		t.Fatalf("got %+v, want message %q in %s as the next frame", message, content, roomID)
	}
}

func TestSameUserMultipleConnections(t *testing.T) {
	server := startTestServer(t)
	server.dialDevices(t, "user-a", 3)

	server.waitForStats(t, Stats{Clients: 3, Users: 1})
	if got := server.hub.Stats().ConnectionsPerUser["user-a"]; got != 3 {
		t.Fatalf("connections_per_user[user-a] = %d, want 3", got)
	}
}

func TestAllDevicesInRoomReceiveMessage(t *testing.T) {
	server := startTestServer(t)
	devices := server.dialDevices(t, "user-a", 3)
	bob := server.dialAs(t, "user-b")
	for _, device := range devices {
		joinRoom(t, device, "room-123")
	}
	joinRoom(t, bob, "room-123")

	sendChat(t, bob, "room-123", "hi all devices")

	for _, device := range devices {
		expectChat(t, device, "room-123", "hi all devices")
	}
}

func TestSenderOtherDevicesReceiveOwnMessage(t *testing.T) {
	server := startTestServer(t)
	devices := server.dialDevices(t, "user-a", 3)
	for _, device := range devices {
		joinRoom(t, device, "room-123")
	}

	sendChat(t, devices[0], "room-123", "sent from laptop")

	for _, device := range devices {
		message := readMessage(t, device)
		if message.Type != messageTypeChat || message.SenderID != "user-a" || message.Content != "sent from laptop" {
			t.Fatalf("got %+v, want own message from user-a", message)
		}
	}
}

func TestClosingOneDeviceKeepsOthers(t *testing.T) {
	server := startTestServer(t)
	devices := server.dialDevices(t, "user-a", 3)
	for _, device := range devices {
		joinRoom(t, device, "room-123")
	}

	closeConnection(t, devices[0])
	server.waitForStats(t, Stats{Clients: 2, Users: 1, Rooms: 1})

	sendChat(t, devices[1], "room-123", "still alive")
	expectChat(t, devices[1], "room-123", "still alive")
	expectChat(t, devices[2], "room-123", "still alive")
}

func TestUserStateCleanedWhenLastDisconnects(t *testing.T) {
	server := startTestServer(t)
	devices := server.dialDevices(t, "user-a", 3)
	for _, device := range devices {
		joinRoom(t, device, "room-123")
	}

	for _, device := range devices {
		closeConnection(t, device)
	}

	server.waitForStats(t, Stats{})
	if perUser := server.hub.Stats().ConnectionsPerUser; len(perUser) != 0 {
		t.Fatalf("connections_per_user = %v, want empty", perUser)
	}
}

func TestConnectionLimitPerUser(t *testing.T) {
	server := startTestServer(t)
	devices := server.dialDevices(t, "user-a", MaxConnectionsPerUser)
	server.waitForStats(t, Stats{Clients: MaxConnectionsPerUser, Users: 1})

	extra := server.dialAs(t, "user-a")
	if err := extra.SetReadDeadline(time.Now().Add(replyTimeout)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	_, _, err := extra.ReadMessage()
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) || closeErr.Code != CloseTooManyConnections {
		t.Fatalf("read error = %v, want close %d", err, CloseTooManyConnections)
	}

	for _, device := range devices {
		joinRoom(t, device, "room-123")
	}
	sendChat(t, devices[0], "room-123", "five still work")
	for _, device := range devices {
		expectChat(t, device, "room-123", "five still work")
	}
	server.waitForStats(t, Stats{Clients: MaxConnectionsPerUser, Users: 1, Rooms: 1})
}

func TestConnectionLimitIsPerUser(t *testing.T) {
	server := startTestServer(t)
	server.dialDevices(t, "user-a", MaxConnectionsPerUser)
	bob := server.dialAs(t, "user-b")

	joinRoom(t, bob, "room-123")
	server.waitForStats(t, Stats{Clients: MaxConnectionsPerUser + 1, Users: 2, Rooms: 1})
}

func TestPresenceOnlyOnFirstAndLast(t *testing.T) {
	server := startTestServer(t)
	bob := server.dialAs(t, "user-b")
	laptop, phone := server.dialAs(t, "user-a"), server.dialAs(t, "user-a")
	joinRoom(t, bob, "room-123")

	joinRoom(t, laptop, "room-123")
	expectPresence(t, bob, "room-123", "user-a", presenceOnline)

	joinRoom(t, phone, "room-123")
	sendChat(t, bob, "room-123", "after second device")
	expectNextFrameIsChat(t, bob, "room-123", "after second device")
	expectNextFrameIsChat(t, laptop, "room-123", "after second device")

	closeConnection(t, laptop)
	server.waitForStats(t, Stats{Clients: 2, Users: 2, Rooms: 1})
	sendChat(t, bob, "room-123", "after first device left")
	expectNextFrameIsChat(t, bob, "room-123", "after first device left")

	closeConnection(t, phone)
	expectPresence(t, bob, "room-123", "user-a", presenceOffline)
}

func TestPresenceIsPerRoom(t *testing.T) {
	server := startTestServer(t)
	bob := server.dialAs(t, "user-b")
	laptop, phone := server.dialAs(t, "user-a"), server.dialAs(t, "user-a")
	joinRoom(t, bob, "room-a")
	joinRoom(t, bob, "room-b")
	joinRoom(t, laptop, "room-a")
	expectPresence(t, bob, "room-a", "user-a", presenceOnline)

	joinRoom(t, phone, "room-b")
	expectPresence(t, bob, "room-b", "user-a", presenceOnline)

	leaveRoom(t, laptop, "room-a")
	expectPresence(t, bob, "room-a", "user-a", presenceOffline)
}

func TestRevokeKicksAllDevices(t *testing.T) {
	server := startAuthorizedServer(t)
	bob := server.dialAs(t, "user-b")
	devices := server.dialDevices(t, "user-a", 3)
	joinRoom(t, bob, "restaurant-001")
	for _, device := range devices {
		joinRoom(t, device, "restaurant-001")
	}
	expectPresence(t, bob, "restaurant-001", "user-a", presenceOnline)

	status, response := server.revoke(t, "restaurant-001", `{"user_id":"user-a"}`)
	if status != http.StatusOK || response.KickedConnections != 3 {
		t.Fatalf("revoke = %d %+v, want 200 with 3 kicked", status, response)
	}
	for _, device := range devices {
		removed := readMessage(t, device)
		if removed.Type != messageTypeRemoved || removed.RoomID != "restaurant-001" {
			t.Fatalf("got %+v, want removed restaurant-001", removed)
		}
	}

	expectPresence(t, bob, "restaurant-001", "user-a", presenceOffline)
	sendChat(t, bob, "restaurant-001", "only one offline")
	expectNextFrameIsChat(t, bob, "restaurant-001", "only one offline")
}

func TestSendToUserReachesEveryDevice(t *testing.T) {
	server := startTestServer(t)
	devices := server.dialDevices(t, "user-a", 2)
	bob := server.dialAs(t, "user-b")
	server.waitForStats(t, Stats{Clients: 3, Users: 2})

	if !server.hub.SendToUser("user-a", []byte(`{"type":"notification","content":"ping"}`)) {
		t.Fatal("SendToUser returned false on a running hub")
	}

	for _, device := range devices {
		message := readMessage(t, device)
		if message.Type != "notification" || message.Content != "ping" {
			t.Fatalf("got %+v, want notification", message)
		}
	}
	expectNoMessage(t, bob, silenceWindow)
}

func TestHubInvariantsUnderChurn(t *testing.T) {
	const (
		workers         = 50
		users           = 10
		actionsPerConn  = 30
		keepEveryNth    = 3
		gracefulRemains = 2
	)
	server := startTestServer(t)
	rooms := []string{"room-a", "room-b", "room-c"}
	tokens := make([]string, users)
	for i := range tokens {
		tokens[i] = server.issueToken(t, fmt.Sprintf("churn-user-%d", i), testTokenTTL)
	}

	var (
		mu    sync.Mutex
		conns []*websocket.Conn
		wg    sync.WaitGroup
	)
	t.Cleanup(func() {
		for _, conn := range conns {
			_ = conn.Close()
		}
	})

	for worker := range workers {
		wg.Go(func() {
			conn, _, err := websocket.DefaultDialer.Dial(server.wsURL, bearerHeader(tokens[worker%users]))
			if err != nil {
				t.Errorf("worker %d dial: %v", worker, err)
				return
			}
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
			go drain(conn)

			rng := rand.New(rand.NewPCG(uint64(worker), 0))
			for range actionsPerConn {
				roomID := rooms[rng.IntN(len(rooms))]
				switch rng.IntN(3) {
				case 0:
					_ = conn.WriteJSON(inboundEnvelope{Type: messageTypeJoinRoom, RoomID: roomID})
				case 1:
					_ = conn.WriteJSON(inboundEnvelope{Type: messageTypeLeaveRoom, RoomID: roomID})
				case 2:
					_ = conn.WriteJSON(inboundEnvelope{Type: messageTypeChat, RoomID: roomID, Content: "churn"})
				}
			}

			switch worker % keepEveryNth {
			case 0:
			case gracefulRemains:
				closeConnection(t, conn)
			default:
				_ = conn.Close()
			}
		})
	}
	wg.Wait()

	survivors := (workers + keepEveryNth - 1) / keepEveryNth
	deadline := time.Now().Add(statsPollTimeout)
	for server.hub.Stats().Clients != survivors {
		if time.Now().After(deadline) {
			t.Fatalf("clients = %d, want %d survivors", server.hub.Stats().Clients, survivors)
		}
		time.Sleep(10 * time.Millisecond)
	}

	server.stopHub()
	if err := server.hub.checkInvariants(); err != nil {
		t.Fatal(err)
	}
}

func drain(conn *websocket.Conn) {
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}
