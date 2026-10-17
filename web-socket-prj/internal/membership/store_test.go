package membership

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestLoadFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memberships.json")
	content := `{"restaurant-001":["user-a","user-b"],"restaurant-002":["user-b"]}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	store, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	cases := []struct {
		userID, roomID string
		want           bool
	}{
		{"user-a", "restaurant-001", true},
		{"user-b", "restaurant-002", true},
		{"user-a", "restaurant-002", false},
		{"user-a", "restaurant-999", false},
	}
	for _, tc := range cases {
		got, err := store.IsMember(context.Background(), tc.userID, tc.roomID)
		if err != nil || got != tc.want {
			t.Errorf("IsMember(%s, %s) = %v, %v; want %v", tc.userID, tc.roomID, got, err, tc.want)
		}
	}
}

func TestLoadFromFileRejectsInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memberships.json")
	if err := os.WriteFile(path, []byte(`["not","a","map"]`), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if _, err := LoadFromFile(path); err == nil {
		t.Fatal("load succeeded, want error")
	}
}

func TestLoadFromFileMissing(t *testing.T) {
	if _, err := LoadFromFile(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("load succeeded, want error")
	}
}

func TestRevoke(t *testing.T) {
	store := NewInMemoryStore(map[string][]string{"restaurant-001": {"user-a", "user-b"}})

	store.Revoke("user-a", "restaurant-001")
	store.Revoke("user-a", "unknown-room")

	if ok, _ := store.IsMember(context.Background(), "user-a", "restaurant-001"); ok {
		t.Fatal("user-a still member after revoke")
	}
	if ok, _ := store.IsMember(context.Background(), "user-b", "restaurant-001"); !ok {
		t.Fatal("revoke removed the wrong user")
	}
}

func TestIsMemberHonorsCanceledContext(t *testing.T) {
	store := NewInMemoryStore(map[string][]string{"restaurant-001": {"user-a"}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ok, err := store.IsMember(ctx, "user-a", "restaurant-001")
	if err == nil || ok {
		t.Fatalf("IsMember = %v, %v; want false with error", ok, err)
	}
}

func TestConcurrentReadAndRevoke(t *testing.T) {
	store := NewInMemoryStore(map[string][]string{"restaurant-001": {"user-a", "user-b"}})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 1000 {
				_, _ = store.IsMember(context.Background(), "user-b", "restaurant-001")
			}
		})
	}
	wg.Go(func() {
		for range 1000 {
			store.Revoke("user-a", "restaurant-001")
		}
	})
	wg.Wait()
}
