package auth

import (
	"errors"
	"net/http"
	"net/mail"
	"strings"

	"github.com/ninhdata/go-api-redis/internal/httpx"
)

const minPasswordLength = 8

type Handler struct {
	service *Service
	store   *Store
}

func NewHandler(service *Service, store *Store) *Handler {
	return &Handler{service: service, store: store}
}

type credentialsRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type logoutRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type sessionResponse struct {
	User   User      `json:"user"`
	Tokens TokenPair `json:"tokens"`
}

func (c credentialsRequest) validate() error {
	if _, err := mail.ParseAddress(strings.TrimSpace(c.Email)); err != nil {
		return errors.New("email is not valid")
	}
	if len(c.Password) < minPasswordLength {
		return errors.New("password must be at least 8 characters")
	}
	return nil
}

func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var request credentialsRequest
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	if err := request.validate(); err != nil {
		httpx.WriteError(w, http.StatusUnprocessableEntity, "validation_failed", err.Error())
		return
	}

	user, tokens, err := h.service.Register(r.Context(), request.Email, request.Password)
	if errors.Is(err, ErrEmailTaken) {
		httpx.WriteError(w, http.StatusConflict, "email_taken", "This email is already registered.")
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "register_failed", "Cannot create the account.")
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, sessionResponse{User: user, Tokens: tokens})
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var request credentialsRequest
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}

	user, tokens, err := h.service.Login(r.Context(), request.Email, request.Password)
	if errors.Is(err, ErrInvalidCredentials) {
		httpx.WriteError(w, http.StatusUnauthorized, "invalid_credentials", "Email or password is incorrect.")
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "login_failed", "Cannot sign in right now.")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, sessionResponse{User: user, Tokens: tokens})
}

func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	var request refreshRequest
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	if request.RefreshToken == "" {
		httpx.WriteError(w, http.StatusUnprocessableEntity, "validation_failed", "refresh_token is required")
		return
	}

	tokens, err := h.service.Refresh(r.Context(), request.RefreshToken)
	switch {
	case errors.Is(err, ErrTokenReused):
		httpx.WriteError(w, http.StatusUnauthorized, "token_reused",
			"This refresh token was already used. All sessions have been revoked.")
	case errors.Is(err, ErrNoSession), errors.Is(err, ErrInvalidToken), errors.Is(err, ErrExpiredToken):
		httpx.WriteError(w, http.StatusUnauthorized, "invalid_refresh_token", "Refresh token is not valid.")
	case err != nil:
		httpx.WriteError(w, http.StatusInternalServerError, "refresh_failed", "Cannot refresh the session.")
	default:
		httpx.WriteJSON(w, http.StatusOK, tokens)
	}
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	identity, _ := IdentityFrom(r.Context())

	var request logoutRequest
	if r.ContentLength > 0 {
		if err := httpx.DecodeJSON(w, r, &request); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_body", err.Error())
			return
		}
	}

	if err := h.service.Logout(r.Context(), identity, request.RefreshToken); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "logout_failed", "Cannot sign out right now.")
		return
	}

	httpx.WriteJSON(w, http.StatusNoContent, nil)
}

func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	identity, _ := IdentityFrom(r.Context())

	user, err := h.store.UserByID(r.Context(), identity.UserID)
	if errors.Is(err, ErrUserNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "user_not_found", "Account no longer exists.")
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "profile_failed", "Cannot load the profile.")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, user)
}
