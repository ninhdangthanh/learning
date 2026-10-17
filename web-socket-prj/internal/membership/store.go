package membership

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

type Store interface {
	IsMember(ctx context.Context, userID, roomID string) (bool, error)
}

type InMemoryStore struct {
	mu      sync.RWMutex
	members map[string]map[string]struct{}
}

func NewInMemoryStore(usersByRoom map[string][]string) *InMemoryStore {
	members := make(map[string]map[string]struct{}, len(usersByRoom))
	for roomID, userIDs := range usersByRoom {
		users := make(map[string]struct{}, len(userIDs))
		for _, userID := range userIDs {
			users[userID] = struct{}{}
		}
		members[roomID] = users
	}
	return &InMemoryStore{members: members}
}

func LoadFromFile(path string) (*InMemoryStore, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read memberships: %w", err)
	}
	var usersByRoom map[string][]string
	if err := json.Unmarshal(raw, &usersByRoom); err != nil {
		return nil, fmt.Errorf("parse memberships %s: %w", path, err)
	}
	return NewInMemoryStore(usersByRoom), nil
}

func (s *InMemoryStore) IsMember(ctx context.Context, userID, roomID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.members[roomID][userID]
	return ok, nil
}

func (s *InMemoryStore) Revoke(userID, roomID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	users, exists := s.members[roomID]
	if !exists {
		return
	}
	delete(users, userID)
	if len(users) == 0 {
		delete(s.members, roomID)
	}
}
