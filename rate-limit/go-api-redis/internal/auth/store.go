package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var (
	ErrEmailTaken   = errors.New("email already registered")
	ErrUserNotFound = errors.New("user not found")
	ErrTokenReused  = errors.New("refresh token reuse detected")
	ErrNoSession    = errors.New("refresh session not found")
)

type User struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	Role         string    `json:"role"`
	CreatedAt    time.Time `json:"created_at"`
	passwordHash string
}

var consumeRefreshScript = redis.NewScript(`
local session = KEYS[1]
local index = KEYS[2]
local used = KEYS[3]
local jti = ARGV[1]
local used_ttl = tonumber(ARGV[2])

if redis.call('EXISTS', used) == 1 then
  return 2
end
if redis.call('EXISTS', session) == 0 then
  return 0
end

redis.call('DEL', session)
redis.call('SREM', index, jti)
redis.call('SET', used, '1', 'PX', used_ttl)
return 1
`)

var revokeSessionsScript = redis.NewScript(`
local index = KEYS[1]
local members = redis.call('SMEMBERS', index)
for i = 1, #members do
  redis.call('DEL', 'refresh:' .. members[i])
end
redis.call('DEL', index)
return #members
`)

type Store struct {
	client *redis.Client
}

func NewStore(client *redis.Client) *Store {
	return &Store{client: client}
}

func userKey(id string) string          { return "user:" + id }
func emailKey(email string) string      { return "user:email:" + email }
func sessionKey(jti string) string      { return "refresh:" + jti }
func sessionIndexKey(id string) string  { return "user:" + id + ":refresh" }
func usedRefreshKey(jti string) string  { return "refresh:used:" + jti }
func deniedAccessKey(jti string) string { return "deny:access:" + jti }

func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func (s *Store) CreateUser(ctx context.Context, email, password string) (User, error) {
	email = NormalizeEmail(email)
	passwordHash, err := hashPassword(password)
	if err != nil {
		return User{}, err
	}

	user := User{
		ID:           uuid.NewString(),
		Email:        email,
		Role:         "user",
		CreatedAt:    time.Now().UTC(),
		passwordHash: passwordHash,
	}

	reserved, err := s.client.SetNX(ctx, emailKey(email), user.ID, 0).Result()
	if err != nil {
		return User{}, err
	}
	if !reserved {
		return User{}, ErrEmailTaken
	}

	err = s.client.HSet(ctx, userKey(user.ID), map[string]any{
		"id":            user.ID,
		"email":         user.Email,
		"role":          user.Role,
		"password_hash": user.passwordHash,
		"created_at":    user.CreatedAt.Format(time.RFC3339),
	}).Err()
	if err != nil {
		s.client.Del(ctx, emailKey(email))
		return User{}, err
	}

	return user, nil
}

func (s *Store) UserByEmail(ctx context.Context, email string) (User, error) {
	id, err := s.client.Get(ctx, emailKey(NormalizeEmail(email))).Result()
	if errors.Is(err, redis.Nil) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, err
	}
	return s.UserByID(ctx, id)
}

func (s *Store) UserByID(ctx context.Context, id string) (User, error) {
	fields, err := s.client.HGetAll(ctx, userKey(id)).Result()
	if err != nil {
		return User{}, err
	}
	if len(fields) == 0 {
		return User{}, ErrUserNotFound
	}

	createdAt, _ := time.Parse(time.RFC3339, fields["created_at"])
	return User{
		ID:           fields["id"],
		Email:        fields["email"],
		Role:         fields["role"],
		CreatedAt:    createdAt,
		passwordHash: fields["password_hash"],
	}, nil
}

func (s *Store) SaveSession(ctx context.Context, userID, jti string, ttl time.Duration) error {
	pipe := s.client.TxPipeline()
	pipe.HSet(ctx, sessionKey(jti), map[string]any{
		"user_id":    userID,
		"created_at": time.Now().UTC().Format(time.RFC3339),
	})
	pipe.Expire(ctx, sessionKey(jti), ttl)
	pipe.SAdd(ctx, sessionIndexKey(userID), jti)
	pipe.Expire(ctx, sessionIndexKey(userID), ttl)
	_, err := pipe.Exec(ctx)
	return err
}

func (s *Store) ConsumeSession(ctx context.Context, userID, jti string, ttl time.Duration) error {
	outcome, err := consumeRefreshScript.Run(
		ctx,
		s.client,
		[]string{sessionKey(jti), sessionIndexKey(userID), usedRefreshKey(jti)},
		jti,
		ttl.Milliseconds(),
	).Int64()
	if err != nil {
		return err
	}

	switch outcome {
	case 1:
		return nil
	case 2:
		return ErrTokenReused
	default:
		return ErrNoSession
	}
}

func (s *Store) RevokeAllSessions(ctx context.Context, userID string) error {
	return revokeSessionsScript.Run(ctx, s.client, []string{sessionIndexKey(userID)}).Err()
}

func (s *Store) DenyAccessToken(ctx context.Context, jti string, ttl time.Duration) error {
	if ttl <= 0 {
		return nil
	}
	return s.client.Set(ctx, deniedAccessKey(jti), "1", ttl).Err()
}

func (s *Store) IsAccessTokenDenied(ctx context.Context, jti string) (bool, error) {
	count, err := s.client.Exists(ctx, deniedAccessKey(jti)).Result()
	if err != nil {
		return false, fmt.Errorf("check denylist: %w", err)
	}
	return count > 0, nil
}
