package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestService(t *testing.T) *Service {
	t.Helper()

	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	tokens := NewTokenManager(TokenManagerConfig{
		Secret:     []byte("test-secret-value-32-characters"),
		Issuer:     "test",
		Audience:   "test-clients",
		AccessTTL:  time.Minute,
		RefreshTTL: time.Hour,
	})

	return NewService(NewStore(client), tokens)
}

func TestRegisterRejectsDuplicateEmail(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()

	if _, _, err := service.Register(ctx, "user@example.com", "password123"); err != nil {
		t.Fatalf("first register: %v", err)
	}

	_, _, err := service.Register(ctx, "USER@example.com", "password123")
	if !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("expected ErrEmailTaken, got %v", err)
	}
}

func TestLoginRejectsWrongPassword(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()

	if _, _, err := service.Register(ctx, "user@example.com", "password123"); err != nil {
		t.Fatalf("register: %v", err)
	}

	if _, _, err := service.Login(ctx, "user@example.com", "wrong-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestRefreshIssuesNewPair(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()

	_, pair, err := service.Register(ctx, "user@example.com", "password123")
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	next, err := service.Refresh(ctx, pair.RefreshToken)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if next.AccessToken == pair.AccessToken || next.RefreshToken == pair.RefreshToken {
		t.Fatal("refresh should hand back a brand new pair")
	}
}

func TestRefreshTokenStaysValidAfterUse(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()

	_, pair, err := service.Register(ctx, "user@example.com", "password123")
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	if _, err := service.Refresh(ctx, pair.RefreshToken); err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	if _, err := service.Refresh(ctx, pair.RefreshToken); err != nil {
		t.Fatalf("the same refresh token must still work, got %v", err)
	}
}

func TestRefreshRejectsUnknownUser(t *testing.T) {
	service := newTestService(t)

	token, _, err := service.tokens.Issue("missing-user", "user", TokenTypeRefresh)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	if _, err := service.Refresh(context.Background(), token); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
}

func TestParseRejectsWrongTokenType(t *testing.T) {
	service := newTestService(t)

	_, pair, err := service.Register(context.Background(), "user@example.com", "password123")
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	if _, err := service.tokens.Parse(pair.AccessToken, TokenTypeRefresh); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("an access token must not pass as a refresh token, got %v", err)
	}
}
