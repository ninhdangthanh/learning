package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"video-call-backend/internal/database"
	"video-call-backend/internal/models"

	"github.com/golang-jwt/jwt/v5"
)

func newTestHandler(t *testing.T) *Handler {
	t.Helper()
	db, err := database.InitDB(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewHandler(db)
}

func doJSON(t *testing.T, handlerFunc http.HandlerFunc, method, path string, body any, token string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	handlerFunc(rec, req)
	return rec
}

func registerUser(t *testing.T, h *Handler, username string) models.AuthResponse {
	t.Helper()
	rec := doJSON(t, h.Register, http.MethodPost, "/api/auth/register", models.RegisterRequest{
		Username: username,
		Email:    username + "@example.com",
		Password: "secret123",
	}, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("register %s: status %d, body %s", username, rec.Code, rec.Body)
	}
	var resp models.AuthResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode register response: %v", err)
	}
	return resp
}

func signToken(t *testing.T, method jwt.SigningMethod, key any, claims jwt.MapClaims) string {
	t.Helper()
	token, err := jwt.NewWithClaims(method, claims).SignedString(key)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return token
}

func TestRegisterValidation(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{"malformed json", `{"username":`, http.StatusBadRequest},
		{"missing username", `{"email":"a@b.c","password":"secret123"}`, http.StatusBadRequest},
		{"missing email", `{"username":"a","password":"secret123"}`, http.StatusBadRequest},
		{"missing password", `{"username":"a","email":"a@b.c"}`, http.StatusBadRequest},
		{"password too short", `{"username":"a","email":"a@b.c","password":"12345"}`, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestHandler(t)
			req := httptest.NewRequest(http.MethodPost, "/api/auth/register", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			h.Register(rec, req)
			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d (body %s)", rec.Code, tt.wantStatus, rec.Body)
			}
		})
	}
}

func TestRegisterSuccessDefaultsDisplayNameAndHidesPassword(t *testing.T) {
	h := newTestHandler(t)
	rec := doJSON(t, h.Register, http.MethodPost, "/api/auth/register", models.RegisterRequest{
		Username: "alice",
		Email:    "alice@example.com",
		Password: "secret123",
	}, "")

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusCreated)
	}
	if strings.Contains(rec.Body.String(), "password") {
		t.Errorf("response leaks password field: %s", rec.Body)
	}

	var resp models.AuthResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.User.DisplayName != "alice" {
		t.Errorf("display_name = %q, want fallback to username", resp.User.DisplayName)
	}

	userID, username, err := ParseToken(resp.Token)
	if err != nil {
		t.Fatalf("returned token is not valid: %v", err)
	}
	if userID != resp.User.ID || username != "alice" {
		t.Errorf("token claims = (%d, %q), want (%d, %q)", userID, username, resp.User.ID, "alice")
	}
}

func TestRegisterDuplicateReturnsConflict(t *testing.T) {
	h := newTestHandler(t)
	registerUser(t, h, "alice")

	rec := doJSON(t, h.Register, http.MethodPost, "/api/auth/register", models.RegisterRequest{
		Username: "alice",
		Email:    "another@example.com",
		Password: "secret123",
	}, "")
	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
}

func TestLogin(t *testing.T) {
	h := newTestHandler(t)
	registerUser(t, h, "alice")

	tests := []struct {
		name       string
		req        models.LoginRequest
		wantStatus int
	}{
		{"valid credentials", models.LoginRequest{Username: "alice", Password: "secret123"}, http.StatusOK},
		{"wrong password", models.LoginRequest{Username: "alice", Password: "wrong-pass"}, http.StatusUnauthorized},
		{"unknown user", models.LoginRequest{Username: "nobody", Password: "secret123"}, http.StatusUnauthorized},
		{"empty fields", models.LoginRequest{}, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := doJSON(t, h.Login, http.MethodPost, "/api/auth/login", tt.req, "")
			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d (body %s)", rec.Code, tt.wantStatus, rec.Body)
			}
		})
	}
}

func TestLoginUsesSameMessageForUnknownUserAndWrongPassword(t *testing.T) {
	h := newTestHandler(t)
	registerUser(t, h, "alice")

	wrongPassword := doJSON(t, h.Login, http.MethodPost, "/api/auth/login", models.LoginRequest{Username: "alice", Password: "wrong-pass"}, "")
	unknownUser := doJSON(t, h.Login, http.MethodPost, "/api/auth/login", models.LoginRequest{Username: "nobody", Password: "secret123"}, "")

	if wrongPassword.Body.String() != unknownUser.Body.String() {
		t.Errorf("responses differ, enabling user enumeration: %q vs %q", wrongPassword.Body, unknownUser.Body)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	h := newTestHandler(t)
	tests := []struct {
		name    string
		handler http.HandlerFunc
		method  string
	}{
		{"register GET", h.Register, http.MethodGet},
		{"login GET", h.Login, http.MethodGet},
		{"me POST", h.Me, http.MethodPost},
		{"users POST", h.ListUsers, http.MethodPost},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := doJSON(t, tt.handler, tt.method, "/", nil, "")
			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
			}
		})
	}
}

func TestMeRequiresValidToken(t *testing.T) {
	h := newTestHandler(t)
	alice := registerUser(t, h, "alice")

	expired := signToken(t, jwt.SigningMethodHS256, jwtSecret, jwt.MapClaims{
		"user_id": float64(alice.User.ID),
		"exp":     time.Now().Add(-time.Hour).Unix(),
	})
	wrongSecret := signToken(t, jwt.SigningMethodHS256, []byte("not-the-secret"), jwt.MapClaims{
		"user_id": float64(alice.User.ID),
		"exp":     time.Now().Add(time.Hour).Unix(),
	})
	deletedUser := signToken(t, jwt.SigningMethodHS256, jwtSecret, jwt.MapClaims{
		"user_id": float64(9999),
		"exp":     time.Now().Add(time.Hour).Unix(),
	})

	tests := []struct {
		name       string
		token      string
		wantStatus int
	}{
		{"valid token", alice.Token, http.StatusOK},
		{"no token", "", http.StatusUnauthorized},
		{"garbage token", "not.a.jwt", http.StatusUnauthorized},
		{"expired token", expired, http.StatusUnauthorized},
		{"signed with wrong secret", wrongSecret, http.StatusUnauthorized},
		{"user no longer exists", deletedUser, http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := doJSON(t, h.Me, http.MethodGet, "/api/auth/me", nil, tt.token)
			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d (body %s)", rec.Code, tt.wantStatus, rec.Body)
			}
		})
	}
}

func TestParseTokenRejectsMalformedClaims(t *testing.T) {
	future := time.Now().Add(time.Hour).Unix()
	tests := []struct {
		name  string
		token string
	}{
		{"missing user_id", signToken(t, jwt.SigningMethodHS256, jwtSecret, jwt.MapClaims{"exp": future})},
		{"user_id is a string", signToken(t, jwt.SigningMethodHS256, jwtSecret, jwt.MapClaims{"user_id": "1", "exp": future})},
		{"unexpected signing algorithm", signToken(t, jwt.SigningMethodHS512, jwtSecret, jwt.MapClaims{"user_id": float64(1), "exp": future})},
		{"alg none", signToken(t, jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, jwt.MapClaims{"user_id": float64(1), "exp": future})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := ParseToken(tt.token); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestListUsers(t *testing.T) {
	h := newTestHandler(t)
	alice := registerUser(t, h, "alice")
	registerUser(t, h, "bob")

	t.Run("requires auth", func(t *testing.T) {
		rec := doJSON(t, h.ListUsers, http.MethodGet, "/api/users", nil, "")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})

	t.Run("returns all users without sensitive fields", func(t *testing.T) {
		rec := doJSON(t, h.ListUsers, http.MethodGet, "/api/users", nil, alice.Token)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if strings.Contains(rec.Body.String(), "email") || strings.Contains(rec.Body.String(), "password") {
			t.Errorf("user list leaks sensitive fields: %s", rec.Body)
		}
		var users []models.UserListItem
		if err := json.NewDecoder(rec.Body).Decode(&users); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(users) != 2 {
			t.Errorf("got %d users, want 2", len(users))
		}
	})
}
