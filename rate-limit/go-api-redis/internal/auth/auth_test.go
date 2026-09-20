package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestService(t *testing.T) (*Service, *Store) {
	t.Helper()

	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	store := NewStore(client)
	tokens := NewTokenManager(TokenManagerConfig{
		Secret:     []byte("test-secret-value-32-characters"),
		Issuer:     "test",
		Audience:   "test-clients",
		AccessTTL:  time.Minute,
		RefreshTTL: time.Hour,
	})

	return NewService(store, tokens), store
}

func TestRegisterRejectsDuplicateEmail(t *testing.T) {
	service, _ := newTestService(t)
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
	service, _ := newTestService(t)
	ctx := context.Background()

	if _, _, err := service.Register(ctx, "user@example.com", "password123"); err != nil {
		t.Fatalf("register: %v", err)
	}

	if _, _, err := service.Login(ctx, "user@example.com", "wrong-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestRefreshRotatesTokens(t *testing.T) {
	service, _ := newTestService(t)
	ctx := context.Background()

	_, pair, err := service.Register(ctx, "user@example.com", "password123")
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	rotated, err := service.Refresh(ctx, pair.RefreshToken)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if rotated.RefreshToken == pair.RefreshToken {
		t.Fatal("refresh token should be rotated")
	}
}

func TestRefreshReuseRevokesEverySession(t *testing.T) {
	service, _ := newTestService(t)
	ctx := context.Background()

	_, pair, err := service.Register(ctx, "user@example.com", "password123")
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	rotated, err := service.Refresh(ctx, pair.RefreshToken)
	if err != nil {
		t.Fatalf("first refresh: %v", err)
	}

	if _, err := service.Refresh(ctx, pair.RefreshToken); !errors.Is(err, ErrTokenReused) {
		t.Fatalf("expected ErrTokenReused, got %v", err)
	}

	if _, err := service.Refresh(ctx, rotated.RefreshToken); !errors.Is(err, ErrNoSession) {
		t.Fatalf("rotated token should be revoked too, got %v", err)
	}
}

func TestLogoutDeniesAccessToken(t *testing.T) {
	service, store := newTestService(t)
	ctx := context.Background()

	_, pair, err := service.Register(ctx, "user@example.com", "password123")
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	claims, err := service.tokens.Parse(pair.AccessToken, TokenTypeAccess)
	if err != nil {
		t.Fatalf("parse access token: %v", err)
	}

	identity := Identity{UserID: claims.Subject, AccessID: claims.ID}
	if err := service.Logout(ctx, identity, pair.RefreshToken); err != nil {
		t.Fatalf("logout: %v", err)
	}

	denied, err := store.IsAccessTokenDenied(ctx, claims.ID)
	if err != nil {
		t.Fatalf("denylist lookup: %v", err)
	}
	if !denied {
		t.Fatal("access token should be on the denylist after logout")
	}

	if _, err := service.Refresh(ctx, pair.RefreshToken); err == nil {
		t.Fatal("refresh token should not work after logout")
	}
}

func TestParseRejectsWrongTokenType(t *testing.T) {
	service, _ := newTestService(t)

	_, pair, err := service.Register(context.Background(), "user@example.com", "password123")
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	if _, err := service.tokens.Parse(pair.AccessToken, TokenTypeRefresh); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("an access token must not pass as a refresh token, got %v", err)
	}
}
