package signaling

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"video-call-backend/internal/auth"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins in development
	},
}

// SignalMessage represents a WebRTC signaling message
type SignalMessage struct {
	Type     string          `json:"type"`     // "offer", "answer", "ice-candidate", "call", "call-accepted", "call-rejected", "call-ended", "user-online", "user-offline", "users-list"
	From     int64           `json:"from"`     // Sender user ID
	To       int64           `json:"to"`       // Receiver user ID
	Payload  json.RawMessage `json:"payload"`  // SDP or ICE candidate data
	FromName string          `json:"fromName"` // Sender display name
}

// Client represents a connected WebSocket client
type Client struct {
	Hub         *Hub
	Conn        *websocket.Conn
	UserID      int64
	Username    string
	DisplayName string
	Send        chan []byte
}

// Hub manages all WebSocket connections and message routing
type Hub struct {
	db         *sql.DB
	clients    map[int64]*Client // userID -> Client
	register   chan *Client
	unregister chan *Client
	broadcast  chan []byte
	mu         sync.RWMutex
}

func NewHub(db *sql.DB) *Hub {
	return &Hub{
		db:         db,
		clients:    make(map[int64]*Client),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		broadcast:  make(chan []byte),
	}
}

func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client.UserID] = client
			h.mu.Unlock()

			log.Printf("✅ User %s (ID: %d) connected", client.DisplayName, client.UserID)

			// Notify all users about the new online user
			h.broadcastOnlineUsers()

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client.UserID]; ok {
				delete(h.clients, client.UserID)
				close(client.Send)
			}
			h.mu.Unlock()

			log.Printf("❌ User %s (ID: %d) disconnected", client.DisplayName, client.UserID)

			// Notify all users about the offline user
			h.broadcastOnlineUsers()
		}
	}
}

func (h *Hub) broadcastOnlineUsers() {
	h.mu.RLock()
	defer h.mu.RUnlock()

	onlineUsers := make([]map[string]interface{}, 0)
	for _, client := range h.clients {
		onlineUsers = append(onlineUsers, map[string]interface{}{
			"id":           client.UserID,
			"username":     client.Username,
			"display_name": client.DisplayName,
			"online":       true,
		})
	}

	payload, _ := json.Marshal(onlineUsers)
	msg := SignalMessage{
		Type:    "users-list",
		Payload: payload,
	}

	data, _ := json.Marshal(msg)
	for _, client := range h.clients {
		select {
		case client.Send <- data:
		default:
			close(client.Send)
			delete(h.clients, client.UserID)
		}
	}
}

func (h *Hub) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Extract token from query parameter
	tokenString := r.URL.Query().Get("token")
	if tokenString == "" {
		http.Error(w, "Token required", http.StatusUnauthorized)
		return
	}

	userID, username, err := auth.ParseToken(tokenString)
	if err != nil {
		http.Error(w, "Invalid token", http.StatusUnauthorized)
		return
	}

	// Get display name from database
	var displayName string
	err = h.db.QueryRowContext(r.Context(), "SELECT display_name FROM users WHERE id = ?", userID).Scan(&displayName)
	if err != nil {
		displayName = username
	}

	// Upgrade to WebSocket
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}

	client := &Client{
		Hub:         h,
		Conn:        conn,
		UserID:      userID,
		Username:    username,
		DisplayName: displayName,
		Send:        make(chan []byte, 256),
	}

	h.register <- client

	go client.writePump()
	go client.readPump()
}

func (c *Client) readPump() {
	defer func() {
		c.Hub.unregister <- c
		c.Conn.Close()
	}()

	for {
		_, message, err := c.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket error: %v", err)
			}
			break
		}

		var msg SignalMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			log.Printf("Failed to parse message: %v", err)
			continue
		}

		// Set sender info
		msg.From = c.UserID
		msg.FromName = c.DisplayName

		// Route the message to the target user
		c.Hub.routeMessage(msg)
	}
}

func (c *Client) writePump() {
	defer c.Conn.Close()

	for message := range c.Send {
		if err := c.Conn.WriteMessage(websocket.TextMessage, message); err != nil {
			return
		}
	}
}

func (h *Hub) routeMessage(msg SignalMessage) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}

	switch msg.Type {
	case "offer", "answer", "ice-candidate", "call", "call-accepted", "call-rejected", "call-ended":
		// Route to specific user
		h.mu.RLock()
		if client, ok := h.clients[msg.To]; ok {
			select {
			case client.Send <- data:
			default:
			}
		}
		h.mu.RUnlock()

	default:
		log.Printf("Unknown message type: %s", msg.Type)
	}
}

// IsUserOnline checks if a user is currently connected
func (h *Hub) IsUserOnline(userID int64) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.clients[userID]
	return ok
}
