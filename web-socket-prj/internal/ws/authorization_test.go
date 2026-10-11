package ws

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ninhdang/ws-chat/internal/membership"
)

func seededMemberships() *membership.InMemoryStore {
	return membership.NewInMemoryStore(map[string][]string{
		"restaurant-001": {"user-a", "user-b"},
		"restaurant-002": {"user-b"},
		"restaurant-003": {"user-a", "user-c"},
	})
}

func startAuthorizedServer(t *testing.T) testServer {
	t.Helper()
	store := seededMemberships()
	return startTestServerWith(t, store, store)
}

func (s testServer) revoke(t *testing.T, roomID, body string) (int, revokeResponse) {
	t.Helper()
	resp, err := http.Post(s.httpURL+"/admin/rooms/"+roomID+"/revoke", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("revoke request: %v", err)
	}
	defer resp.Body.Close()
	var decoded revokeResponse
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
			t.Fatalf("decode revoke response: %v", err)
		}
	}
	return resp.StatusCode, decoded
}

func requestJoin(t *testing.T, conn *websocket.Conn, roomID string) {
	t.Helper()
	sendJSON(t, conn, inboundEnvelope{Type: messageTypeJoinRoom, RoomID: roomID})
}

func TestMemberCanJoin(t *testing.T) {
	server := startAuthorizedServer(t)
	alice := server.dialAs(t, "user-a")

	joinRoom(t, alice, "restaurant-001")
	server.waitForStats(t, Stats{Clients: 1, Users: 1, Rooms: 1})
}

func TestNonMemberGetsForbidden(t *testing.T) {
	server := startAuthorizedServer(t)
	alice := server.dialAs(t, "user-a")

	requestJoin(t, alice, "restaurant-002")
	message := expectError(t, alice, ErrorCodeForbidden)
	if message.RoomID != "restaurant-002" || message.RequestType != messageTypeJoinRoom {
		t.Fatalf("error should echo request, got %+v", message)
	}
	server.waitForStats(t, Stats{Clients: 1, Users: 1, Rooms: 0})

	joinRoom(t, alice, "restaurant-001")
}

func TestUnknownRoomGetsForbidden(t *testing.T) {
	server := startAuthorizedServer(t)
	alice := server.dialAs(t, "user-a")

	requestJoin(t, alice, "restaurant-002")
	forbidden := expectError(t, alice, ErrorCodeForbidden)
	requestJoin(t, alice, "restaurant-999")
	unknown := expectError(t, alice, ErrorCodeForbidden)

	forbidden.RoomID, unknown.RoomID = "", ""
	if forbidden != unknown {
		t.Fatalf("unknown room response %+v differs from forbidden %+v", unknown, forbidden)
	}
}

func TestCannotSendToUnauthorizedRoom(t *testing.T) {
	server := startAuthorizedServer(t)
	alice, bob := server.dialAs(t, "user-a"), server.dialAs(t, "user-b")
	joinRoom(t, bob, "restaurant-002")

	sendChat(t, alice, "restaurant-002", "sneaky")
	expectError(t, alice, ErrorCodeNotInRoom)

	requestJoin(t, alice, "restaurant-002")
	expectError(t, alice, ErrorCodeForbidden)
	sendChat(t, alice, "restaurant-002", "sneaky again")
	expectError(t, alice, ErrorCodeNotInRoom)

	expectNoMessage(t, bob, silenceWindow)
}

func TestStoreErrorFailsClosed(t *testing.T) {
	failing := membershipFunc(func(context.Context, string, string) (bool, error) {
		return true, errors.New("store unavailable")
	})
	server := startTestServerWith(t, failing, nil)
	alice := server.dialAs(t, "user-a")

	requestJoin(t, alice, "restaurant-001")
	expectError(t, alice, ErrorCodeInternal)
	server.waitForStats(t, Stats{Clients: 1, Users: 1, Rooms: 0})

	sendChat(t, alice, "restaurant-001", "should not pass")
	expectError(t, alice, ErrorCodeNotInRoom)
}

func TestMembershipCheckUsesDeadline(t *testing.T) {
	deadlines := make(chan time.Duration, 1)
	recording := membershipFunc(func(ctx context.Context, _, _ string) (bool, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			deadlines <- 0
			return false, nil
		}
		deadlines <- time.Until(deadline)
		return true, nil
	})
	server := startTestServerWith(t, recording, nil)
	alice := server.dialAs(t, "user-a")

	joinRoom(t, alice, "restaurant-001")
	remaining := <-deadlines
	if remaining <= 0 || remaining > DefaultConfig().MembershipWait {
		t.Fatalf("membership check deadline = %v, want within (0, %v]", remaining, DefaultConfig().MembershipWait)
	}
}

func TestRevokeKicksFromRoom(t *testing.T) {
	server := startAuthorizedServer(t)
	alicePhone, aliceLaptop, bob := server.dialAs(t, "user-a"), server.dialAs(t, "user-a"), server.dialAs(t, "user-b")
	joinRoom(t, alicePhone, "restaurant-001")
	joinRoom(t, alicePhone, "restaurant-003")
	joinRoom(t, aliceLaptop, "restaurant-001")
	joinRoom(t, bob, "restaurant-001")

	status, response := server.revoke(t, "restaurant-001", `{"user_id":"user-a"}`)
	want := revokeResponse{UserID: "user-a", RoomID: "restaurant-001", KickedConnections: 2}
	if status != http.StatusOK || response != want {
		t.Fatalf("revoke = %d %+v, want 200 %+v", status, response, want)
	}

	for _, conn := range []*websocket.Conn{alicePhone, aliceLaptop} {
		removed := readMessage(t, conn)
		if removed.Type != messageTypeRemoved || removed.RoomID != "restaurant-001" {
			t.Fatalf("got %+v, want removed restaurant-001", removed)
		}
	}

	requestJoin(t, alicePhone, "restaurant-001")
	expectError(t, alicePhone, ErrorCodeForbidden)

	sendChat(t, alicePhone, "restaurant-003", "still allowed here")
	expectChat(t, alicePhone, "restaurant-003", "still allowed here")

	sendChat(t, bob, "restaurant-001", "after revoke")
	expectChat(t, bob, "restaurant-001", "after revoke")
	expectNoMessage(t, alicePhone, silenceWindow)
	expectNoMessage(t, aliceLaptop, silenceWindow)
}

func TestRevokeWithoutActiveConnections(t *testing.T) {
	server := startAuthorizedServer(t)

	status, response := server.revoke(t, "restaurant-003", `{"user_id":"user-c"}`)
	if status != http.StatusOK || response.KickedConnections != 0 {
		t.Fatalf("revoke = %d %+v, want 200 with 0 kicked", status, response)
	}
}

func TestRevokeRejectsBadRequests(t *testing.T) {
	server := startAuthorizedServer(t)
	cases := map[string]struct{ roomID, body string }{
		"MissingUserID": {"restaurant-001", `{}`},
		"BrokenJSON":    {"restaurant-001", `{"user_id":`},
		"InvalidRoomID": {"Bad_Room", `{"user_id":"user-a"}`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if status, _ := server.revoke(t, tc.roomID, tc.body); status != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", status)
			}
		})
	}
}

func TestSlowStoreDoesNotBlockOtherClients(t *testing.T) {
	aliceCheckStarted := make(chan struct{})
	releaseAlice := make(chan struct{})
	slowForAlice := membershipFunc(func(ctx context.Context, userID, _ string) (bool, error) {
		if userID != "user-a" {
			return true, nil
		}
		close(aliceCheckStarted)
		select {
		case <-releaseAlice:
			return true, nil
		case <-ctx.Done():
			return false, ctx.Err()
		}
	})
	server := startTestServerWith(t, slowForAlice, nil)
	alice, bob := server.dialAs(t, "user-a"), server.dialAs(t, "user-b")

	requestJoin(t, alice, "restaurant-001")
	<-aliceCheckStarted

	started := time.Now()
	joinRoom(t, bob, "restaurant-001")
	sendChat(t, bob, "restaurant-001", "hub is free")
	expectChat(t, bob, "restaurant-001", "hub is free")
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("bob waited %v while alice's membership check was pending", elapsed)
	}

	close(releaseAlice)
	ack := readMessage(t, alice)
	if ack.Type != messageTypeJoined || ack.RoomID != "restaurant-001" {
		t.Fatalf("got %+v, want joined restaurant-001", ack)
	}
}
