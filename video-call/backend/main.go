package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"video-call-backend/internal/auth"
	"video-call-backend/internal/database"
	"video-call-backend/internal/signaling"
)

const readHeaderTimeout = 10 * time.Second

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	// Initialize database
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "./videocall.db"
	}
	db, err := database.InitDB(context.Background(), dbPath)
	if err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}
	defer db.Close()

	// Initialize handlers
	authHandler := auth.NewHandler(db)
	signalingHub := signaling.NewHub(db)
	go signalingHub.Run()

	// CORS middleware
	mux := http.NewServeMux()

	// Auth routes
	mux.HandleFunc("/api/auth/register", authHandler.Register)
	mux.HandleFunc("/api/auth/login", authHandler.Login)
	mux.HandleFunc("/api/auth/me", authHandler.Me)

	// User routes
	mux.HandleFunc("/api/users", authHandler.ListUsers)

	// WebSocket signaling
	mux.HandleFunc("/ws", signalingHub.HandleWebSocket)

	// Wrap with CORS
	handler := corsMiddleware(mux)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	server := &http.Server{
		Addr:              ":" + port,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
	}

	log.Printf("🚀 Video Call Backend running on http://localhost:%s", port)
	if err := server.ListenAndServe(); err != nil {
		return fmt.Errorf("server failed: %w", err)
	}
	return nil
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}
