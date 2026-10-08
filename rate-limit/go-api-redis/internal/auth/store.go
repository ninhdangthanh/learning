package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var (
	ErrEmailTaken   = errors.New("email already registered")
	ErrUserNotFound = errors.New("user not found")
)

type User struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	Role         string    `json:"role"`
	CreatedAt    time.Time `json:"created_at"`
	passwordHash string
}

type Store struct {
	client *redis.Client
}

func NewStore(client *redis.Client) *Store {
	return &Store{client: client}
}

func userKey(id string) string     { return "user:" + id }
func emailKey(email string) string { return "user:email:" + email }

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
