package database

import (
	"context"
	"path/filepath"
	"testing"
)

func TestInitDBCreatesUsersTable(t *testing.T) {
	db, err := InitDB(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	var tableName string
	err = db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='users'").Scan(&tableName)
	if err != nil {
		t.Fatalf("users table not found: %v", err)
	}
}

func TestInitDBIsIdempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	first, err := InitDB(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("first InitDB: %v", err)
	}
	_ = first.Close()

	second, err := InitDB(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("second InitDB on existing file: %v", err)
	}
	_ = second.Close()
}

func TestUsersTableEnforcesUniqueUsernameAndEmail(t *testing.T) {
	db, err := InitDB(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	insert := "INSERT INTO users (username, email, password_hash, display_name) VALUES (?, ?, 'hash', 'name')"
	if _, err := db.Exec(insert, "alice", "alice@example.com"); err != nil {
		t.Fatalf("seed insert: %v", err)
	}

	tests := []struct {
		name     string
		username string
		email    string
	}{
		{"duplicate username", "alice", "other@example.com"},
		{"duplicate email", "bob", "alice@example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := db.Exec(insert, tt.username, tt.email); err == nil {
				t.Fatal("expected unique constraint violation, got nil")
			}
		})
	}
}
