package store

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

var ErrNotFound = errors.New("product not found")

type Product struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Price int64  `json:"price"`
	Qty   int32  `json:"qty"`
}

type Store struct {
	mu     sync.RWMutex
	items  map[string]Product
	nextID int
}

func New() *Store {
	s := &Store{items: map[string]Product{}, nextID: 1}
	s.Create("Bàn phím cơ Keychron K2", 2190000, 12)
	s.Create("Chuột Logitech MX Master 3S", 2450000, 30)
	s.Create("Màn hình Dell U2723QE", 12900000, 5)
	return s
}

func (s *Store) newID() string {
	id := fmt.Sprintf("p%03d", s.nextID)
	s.nextID++
	return id
}

func (s *Store) List() []Product {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Product, 0, len(s.items))
	for _, p := range s.items {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *Store) Get(id string) (Product, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	p, ok := s.items[id]
	if !ok {
		return Product{}, ErrNotFound
	}
	return p, nil
}

func (s *Store) Create(name string, price int64, qty int32) Product {
	s.mu.Lock()
	defer s.mu.Unlock()

	p := Product{ID: s.newID(), Name: name, Price: price, Qty: qty}
	s.items[p.ID] = p
	return p
}

func (s *Store) Update(id string, name string, price int64, qty int32) (Product, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.items[id]; !ok {
		return Product{}, ErrNotFound
	}
	p := Product{ID: id, Name: name, Price: price, Qty: qty}
	s.items[id] = p
	return p, nil
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.items[id]; !ok {
		return ErrNotFound
	}
	delete(s.items, id)
	return nil
}
