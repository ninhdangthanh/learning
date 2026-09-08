package rest

import (
	"encoding/json"
	"errors"
	"net/http"

	"grpcvshttp/server/store"
)

type Handler struct {
	store *store.Store
}

func NewHandler(s *store.Store) *Handler {
	return &Handler{store: s}
}

type productPayload struct {
	Name  string `json:"name"`
	Price int64  `json:"price"`
	Qty   int32  `json:"qty"`
}

type errorPayload struct {
	Error string `json:"error"`
}

func (h *Handler) Mux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/products", h.list)
	mux.HandleFunc("POST /api/products", h.create)
	mux.HandleFunc("GET /api/products/{id}", h.get)
	mux.HandleFunc("PUT /api/products/{id}", h.update)
	mux.HandleFunc("DELETE /api/products/{id}", h.delete)
	return withCORS(mux)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.store.List())
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	p, err := h.store.Get(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	body, ok := decodePayload(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusCreated, h.store.Create(body.Name, body.Price, body.Qty))
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	body, ok := decodePayload(w, r)
	if !ok {
		return
	}
	p, err := h.store.Update(r.PathValue("id"), body.Name, body.Price, body.Qty)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	if err := h.store.Delete(r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodePayload(w http.ResponseWriter, r *http.Request) (productPayload, bool) {
	var body productPayload
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorPayload{Error: "body không phải JSON hợp lệ"})
		return productPayload{}, false
	}
	if body.Name == "" {
		writeJSON(w, http.StatusBadRequest, errorPayload{Error: "name không được rỗng"})
		return productPayload{}, false
	}
	return body, true
}

func writeError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, errorPayload{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusInternalServerError, errorPayload{Error: err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if status == http.StatusNoContent {
		return
	}
	json.NewEncoder(w).Encode(body)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
