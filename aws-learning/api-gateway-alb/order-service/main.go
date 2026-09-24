package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
)

type Order struct {
	ID       string `json:"id"`
	Product  string `json:"product"`
	Quantity int    `json:"quantity"`
	Status   string `json:"status"`
}

var (
	mu     sync.RWMutex
	seq    = 2
	orders = map[string]Order{
		"1": {ID: "1", Product: "Ca phe sua", Quantity: 2, Status: "NEW"},
		"2": {ID: "2", Product: "Banh mi", Quantity: 1, Status: "DONE"},
	}
	healthy    atomic.Bool
	instanceID = resolveInstanceID()
)

func resolveInstanceID() string {
	if v := os.Getenv("INSTANCE_ID"); v != "" {
		return v
	}
	host, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return host
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Instance", instanceID)
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func listOrders(w http.ResponseWriter, r *http.Request) {
	mu.RLock()
	defer mu.RUnlock()
	items := make([]Order, 0, len(orders))
	for _, o := range orders {
		items = append(items, o)
	}
	log.Printf("[order] GET /orders -> %d items (instance=%s)", len(items), instanceID)
	writeJSON(w, http.StatusOK, map[string]any{"instance": instanceID, "orders": items})
}

func getOrder(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	mu.RLock()
	o, ok := orders[id]
	mu.RUnlock()
	log.Printf("[order] GET /orders/%s found=%v (instance=%s)", id, ok, instanceID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "order not found", "id": id})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"instance": instanceID, "order": o})
}

func createOrder(w http.ResponseWriter, r *http.Request) {
	var in Order
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	mu.Lock()
	seq++
	in.ID = strconv.Itoa(seq)
	if in.Status == "" {
		in.Status = "NEW"
	}
	orders[in.ID] = in
	mu.Unlock()
	log.Printf("[order] POST /orders id=%s product=%s (instance=%s)", in.ID, in.Product, instanceID)
	writeJSON(w, http.StatusCreated, map[string]any{"instance": instanceID, "order": in})
}

func deleteOrder(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	mu.Lock()
	_, ok := orders[id]
	delete(orders, id)
	mu.Unlock()
	log.Printf("[order] DELETE /orders/%s existed=%v (instance=%s)", id, ok, instanceID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "order not found", "id": id})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"instance": instanceID, "deleted": id})
}

func health(w http.ResponseWriter, r *http.Request) {
	if !healthy.Load() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"instance": instanceID, "status": "unhealthy"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"instance": instanceID, "status": "ok"})
}

func toggleHealth(w http.ResponseWriter, r *http.Request) {
	healthy.Store(!healthy.Load())
	log.Printf("[order] health toggled -> %v (instance=%s)", healthy.Load(), instanceID)
	writeJSON(w, http.StatusOK, map[string]any{"instance": instanceID, "healthy": healthy.Load()})
}

func main() {
	healthy.Store(true)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /orders", listOrders)
	mux.HandleFunc("GET /orders/{id}", getOrder)
	mux.HandleFunc("POST /orders", createOrder)
	mux.HandleFunc("DELETE /orders/{id}", deleteOrder)
	mux.HandleFunc("GET /health", health)
	mux.HandleFunc("POST /admin/toggle-health", toggleHealth)

	log.Printf("order-service listening on :%s (instance=%s)", port, instanceID)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}
