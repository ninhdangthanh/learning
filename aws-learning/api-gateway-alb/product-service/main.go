package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
)

type Product struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Price int    `json:"price"`
}

var (
	mu       sync.RWMutex
	seq      = 3
	products = map[string]Product{
		"1": {ID: "1", Name: "Ca phe sua", Price: 25000},
		"2": {ID: "2", Name: "Banh mi", Price: 20000},
		"3": {ID: "3", Name: "Tra dao", Price: 35000},
	}
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

func listProducts(w http.ResponseWriter, r *http.Request) {
	mu.RLock()
	defer mu.RUnlock()
	items := make([]Product, 0, len(products))
	for _, p := range products {
		items = append(items, p)
	}
	log.Printf("[product] GET /products -> %d items (instance=%s)", len(items), instanceID)
	writeJSON(w, http.StatusOK, map[string]any{"instance": instanceID, "products": items})
}

func getProduct(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	mu.RLock()
	p, ok := products[id]
	mu.RUnlock()
	log.Printf("[product] GET /products/%s found=%v (instance=%s)", id, ok, instanceID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "product not found", "id": id})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"instance": instanceID, "product": p})
}

func createProduct(w http.ResponseWriter, r *http.Request) {
	var in Product
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	mu.Lock()
	seq++
	in.ID = strconv.Itoa(seq)
	products[in.ID] = in
	mu.Unlock()
	log.Printf("[product] POST /products id=%s name=%s (instance=%s)", in.ID, in.Name, instanceID)
	writeJSON(w, http.StatusCreated, map[string]any{"instance": instanceID, "product": in})
}

func health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"instance": instanceID, "status": "ok"})
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /products", listProducts)
	mux.HandleFunc("GET /products/{id}", getProduct)
	mux.HandleFunc("POST /products", createProduct)
	mux.HandleFunc("GET /health", health)

	log.Printf("product-service listening on :%s (instance=%s)", port, instanceID)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}
