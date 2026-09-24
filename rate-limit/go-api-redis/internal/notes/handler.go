package notes

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/ninhdata/go-api-redis/internal/auth"
	"github.com/ninhdata/go-api-redis/internal/httpx"
)

const (
	defaultPageSize = 20
	maxPageSize     = 100
	maxTitleLength  = 200
)

type Handler struct {
	store *Store
}

func NewHandler(store *Store) *Handler {
	return &Handler{store: store}
}

type createRequest struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

type updateRequest struct {
	Title *string `json:"title"`
	Body  *string `json:"body"`
}

type listResponse struct {
	Items  []Note `json:"items"`
	Total  int64  `json:"total"`
	Offset int64  `json:"offset"`
	Limit  int64  `json:"limit"`
}

func (h *Handler) Routes(writeLimiter func(http.Handler) http.Handler) chi.Router {
	router := chi.NewRouter()
	router.Get("/", h.List)
	router.Get("/{noteID}", h.Get)
	router.Group(func(write chi.Router) {
		write.Use(writeLimiter)
		write.Post("/", h.Create)
		write.Patch("/{noteID}", h.Update)
		write.Delete("/{noteID}", h.Delete)
	})
	return router
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	var request createRequest
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}

	title := strings.TrimSpace(request.Title)
	if title == "" || len(title) > maxTitleLength {
		httpx.WriteError(w, http.StatusUnprocessableEntity, "validation_failed",
			"title is required and must be at most 200 characters")
		return
	}

	note, err := h.store.Create(r.Context(), auth.UserIDFrom(r.Context()), title, request.Body)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "create_failed", "Cannot create the note.")
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, note)
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	offset := queryInt(r, "offset", 0, 0)
	limit := queryInt(r, "limit", defaultPageSize, 1)
	if limit > maxPageSize {
		limit = maxPageSize
	}

	items, total, err := h.store.List(r.Context(), auth.UserIDFrom(r.Context()), offset, limit)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "list_failed", "Cannot load notes.")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, listResponse{Items: items, Total: total, Offset: offset, Limit: limit})
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	note, err := h.store.Get(r.Context(), auth.UserIDFrom(r.Context()), chi.URLParam(r, "noteID"))
	if errors.Is(err, ErrNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "note_not_found", "Note does not exist.")
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "get_failed", "Cannot load the note.")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, note)
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	var request updateRequest
	if err := httpx.DecodeJSON(w, r, &request); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	if request.Title == nil && request.Body == nil {
		httpx.WriteError(w, http.StatusUnprocessableEntity, "validation_failed",
			"provide at least one of title or body")
		return
	}
	if request.Title != nil {
		trimmed := strings.TrimSpace(*request.Title)
		if trimmed == "" || len(trimmed) > maxTitleLength {
			httpx.WriteError(w, http.StatusUnprocessableEntity, "validation_failed",
				"title must not be empty and must be at most 200 characters")
			return
		}
		request.Title = &trimmed
	}

	note, err := h.store.Update(r.Context(), auth.UserIDFrom(r.Context()), chi.URLParam(r, "noteID"),
		request.Title, request.Body)
	if errors.Is(err, ErrNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "note_not_found", "Note does not exist.")
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "update_failed", "Cannot update the note.")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, note)
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	err := h.store.Delete(r.Context(), auth.UserIDFrom(r.Context()), chi.URLParam(r, "noteID"))
	if errors.Is(err, ErrNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "note_not_found", "Note does not exist.")
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "delete_failed", "Cannot delete the note.")
		return
	}

	httpx.WriteJSON(w, http.StatusNoContent, nil)
}

func queryInt(r *http.Request, name string, fallback, min int64) int64 {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < min {
		return fallback
	}
	return value
}
