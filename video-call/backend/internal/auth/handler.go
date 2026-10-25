package auth

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"video-call-backend/internal/models"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

var jwtSecret = []byte(getJWTSecretValue())

var ErrInvalidToken = errors.New("invalid token")

func getJWTSecretValue() string {
	if secret := os.Getenv("JWT_SECRET"); secret != "" {
		return secret
	}

	return "video-call-secret-key-change-in-production"
}

type Handler struct {
	db *sql.DB
}

func NewHandler(db *sql.DB) *Handler {
	return &Handler{db: db}
}

func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req models.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
		return
	}

	// Validate input
	if req.Username == "" || req.Email == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Username, email and password are required"})
		return
	}

	if len(req.Password) < 6 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Password must be at least 6 characters"})
		return
	}

	if req.DisplayName == "" {
		req.DisplayName = req.Username
	}

	// Hash password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to hash password"})
		return
	}

	// Insert user
	result, err := h.db.ExecContext(r.Context(),
		"INSERT INTO users (username, email, password_hash, display_name) VALUES (?, ?, ?, ?)",
		req.Username, req.Email, string(hashedPassword), req.DisplayName,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "Username or email already exists"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to create user"})
		return
	}

	userID, _ := result.LastInsertId()

	user := models.User{
		ID:          userID,
		Username:    req.Username,
		Email:       req.Email,
		DisplayName: req.DisplayName,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	// Generate JWT
	token, err := generateToken(user)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to generate token"})
		return
	}

	writeJSON(w, http.StatusCreated, models.AuthResponse{
		Token: token,
		User:  user,
	})
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req models.LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
		return
	}

	if req.Username == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Username and password are required"})
		return
	}

	// Find user
	var user models.User
	err := h.db.QueryRowContext(r.Context(),
		"SELECT id, username, email, password_hash, display_name, avatar_url, created_at, updated_at FROM users WHERE username = ?",
		req.Username,
	).Scan(&user.ID, &user.Username, &user.Email, &user.PasswordHash, &user.DisplayName, &user.AvatarURL, &user.CreatedAt, &user.UpdatedAt)

	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid username or password"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Database error"})
		return
	}

	// Check password
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid username or password"})
		return
	}

	// Generate JWT
	token, err := generateToken(user)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to generate token"})
		return
	}

	writeJSON(w, http.StatusOK, models.AuthResponse{
		Token: token,
		User:  user,
	})
}

func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	user, err := h.getUserFromToken(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
		return
	}

	writeJSON(w, http.StatusOK, user)
}

func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	_, err := h.getUserFromToken(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
		return
	}

	rows, err := h.db.QueryContext(r.Context(), "SELECT id, username, display_name, avatar_url FROM users")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to fetch users"})
		return
	}
	defer rows.Close()

	var users []models.UserListItem
	for rows.Next() {
		var u models.UserListItem
		if err := rows.Scan(&u.ID, &u.Username, &u.DisplayName, &u.AvatarURL); err != nil {
			continue
		}
		users = append(users, u)
	}

	if users == nil {
		users = []models.UserListItem{}
	}

	writeJSON(w, http.StatusOK, users)
}

func (h *Handler) getUserFromToken(r *http.Request) (*models.User, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return nil, ErrInvalidToken
	}

	userID, _, err := ParseToken(strings.TrimPrefix(authHeader, "Bearer "))
	if err != nil {
		return nil, err
	}

	var user models.User
	err = h.db.QueryRowContext(r.Context(),
		"SELECT id, username, email, password_hash, display_name, avatar_url, created_at, updated_at FROM users WHERE id = ?",
		userID,
	).Scan(&user.ID, &user.Username, &user.Email, &user.PasswordHash, &user.DisplayName, &user.AvatarURL, &user.CreatedAt, &user.UpdatedAt)

	if err != nil {
		return nil, err
	}

	return &user, nil
}

func ParseToken(tokenString string) (int64, string, error) {
	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
		return jwtSecret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil || !token.Valid {
		return 0, "", ErrInvalidToken
	}

	rawUserID, ok := claims["user_id"].(float64)
	if !ok {
		return 0, "", ErrInvalidToken
	}
	username, _ := claims["username"].(string)

	return int64(rawUserID), username, nil
}

func generateToken(user models.User) (string, error) {
	claims := jwt.MapClaims{
		"user_id":  user.ID,
		"username": user.Username,
		"exp":      time.Now().Add(24 * time.Hour).Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(jwtSecret)
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("Failed to encode response: %v", err)
	}
}
