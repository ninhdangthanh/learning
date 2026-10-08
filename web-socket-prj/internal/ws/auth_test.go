package ws

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"github.com/ninhdang/ws-chat/internal/auth"
)

func signTestToken(t *testing.T, method jwt.SigningMethod, key any, claims jwt.RegisteredClaims) string {
	t.Helper()
	token, err := jwt.NewWithClaims(method, claims).SignedString(key)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return token
}

func validClaims(userID string) jwt.RegisteredClaims {
	now := time.Now()
	return jwt.RegisteredClaims{
		Subject:   userID,
		Issuer:    auth.DefaultIssuer,
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
	}
}

func expectUnauthorized(t *testing.T, server testServer, header http.Header, wantChallenge string) {
	t.Helper()
	conn, resp, err := server.tryDial(t, server.wsURL, header)
	if err == nil {
		t.Fatal("dial succeeded, want 401")
	}
	if conn != nil || !errors.Is(err, websocket.ErrBadHandshake) {
		t.Fatalf("dial error = %v, want bad handshake", err)
	}
	if statusOf(resp) != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", statusOf(resp))
	}
	if got := resp.Header.Get("WWW-Authenticate"); !strings.Contains(got, wantChallenge) {
		t.Fatalf("WWW-Authenticate = %q, want it to contain %q", got, wantChallenge)
	}
	server.waitForStats(t, Stats{})
}

func TestRejectsMissingToken(t *testing.T) {
	server := startTestServer(t)
	expectUnauthorized(t, server, nil, `Bearer realm="ws-chat"`)
}

func TestRejectsInvalidSignature(t *testing.T) {
	server := startTestServer(t)
	token := signTestToken(t, jwt.SigningMethodHS256, []byte("another-secret-that-is-also-32-bytes-long"), validClaims("alice"))
	expectUnauthorized(t, server, bearerHeader(token), `error="invalid_token"`)
}

func TestRejectsExpiredToken(t *testing.T) {
	server := startTestServer(t)
	claims := validClaims("alice")
	claims.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-auth.ClockSkewLeeway - time.Minute))
	token := signTestToken(t, jwt.SigningMethodHS256, testJWTSecret, claims)
	expectUnauthorized(t, server, bearerHeader(token), `error_description="token expired"`)
}

func TestRejectsAlgNone(t *testing.T) {
	server := startTestServer(t)
	token := signTestToken(t, jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, validClaims("alice"))
	expectUnauthorized(t, server, bearerHeader(token), `error="invalid_token"`)
}

func TestRejectsWrongIssuer(t *testing.T) {
	server := startTestServer(t)
	claims := validClaims("alice")
	claims.Issuer = "another-system"
	token := signTestToken(t, jwt.SigningMethodHS256, testJWTSecret, claims)
	expectUnauthorized(t, server, bearerHeader(token), `error="invalid_token"`)
}

func TestRejectsTokenWithoutExpiry(t *testing.T) {
	server := startTestServer(t)
	claims := validClaims("alice")
	claims.ExpiresAt = nil
	token := signTestToken(t, jwt.SigningMethodHS256, testJWTSecret, claims)
	expectUnauthorized(t, server, bearerHeader(token), `error="invalid_token"`)
}

func TestRejectsTokenWithoutSubject(t *testing.T) {
	server := startTestServer(t)
	token := signTestToken(t, jwt.SigningMethodHS256, testJWTSecret, validClaims(""))
	expectUnauthorized(t, server, bearerHeader(token), `error="invalid_token"`)
}

func TestAcceptsHeaderToken(t *testing.T) {
	server := startTestServer(t)
	token := server.issueToken(t, "alice", testTokenTTL)

	_, resp, err := server.tryDial(t, server.wsURL, http.Header{"Authorization": []string{"bearer " + token}})
	if err != nil || statusOf(resp) != http.StatusSwitchingProtocols {
		t.Fatalf("dial: err=%v status=%d, want 101", err, statusOf(resp))
	}
	server.waitForStats(t, Stats{Clients: 1})
}

func TestAcceptsQueryToken(t *testing.T) {
	server := startTestServer(t)
	token := server.issueToken(t, "alice", testTokenTTL)

	_, resp, err := server.tryDial(t, server.wsURL+"?"+accessTokenQueryParam+"="+url.QueryEscape(token), nil)
	if err != nil || statusOf(resp) != http.StatusSwitchingProtocols {
		t.Fatalf("dial: err=%v status=%d, want 101", err, statusOf(resp))
	}
	server.waitForStats(t, Stats{Clients: 1})
}

func TestSenderIDComesFromToken(t *testing.T) {
	server := startTestServer(t)
	alice := server.dialAs(t, "alice")
	bob := server.dialAs(t, "bob")
	joinRoom(t, alice, "room-123")
	joinRoom(t, bob, "room-123")

	sendJSON(t, alice, map[string]string{
		"type":      messageTypeChat,
		"room_id":   "room-123",
		"content":   "trust me",
		"sender_id": "hacker",
	})

	for _, conn := range []*websocket.Conn{alice, bob} {
		message := readMessage(t, conn)
		if message.Type != messageTypeChat || message.SenderID != "alice" {
			t.Fatalf("got %+v, want message from alice", message)
		}
	}
}

func TestConnectionClosedWhenTokenExpires(t *testing.T) {
	server := startTestServer(t)
	conn := server.dialWithToken(t, server.issueToken(t, "alice", time.Second))
	joinRoom(t, conn, "room-123")

	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	_, _, err := conn.ReadMessage()

	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) || closeErr.Code != CloseTokenExpired {
		t.Fatalf("read error = %v, want close %d", err, CloseTokenExpired)
	}
	server.waitForStats(t, Stats{})
}
