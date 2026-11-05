package store

import "sync"

// Store is an in-memory key-value store, safe for concurrent use.
type Store struct {
	mu   sync.RWMutex
	data map[string]string
}

func New() *Store {
	return &Store{
		data: make(map[string]string),
	}
}

// Set stores value under key, overwriting any previous value.
func (s *Store) Set(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data[key] = value
}

// Get returns the value for key. ok is false if the key does not exist,
// which lets callers tell a missing key from an empty value.
func (s *Store) Get(key string) (value string, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	value, ok = s.data[key]
	return value, ok
}

// Del removes the given keys and returns how many of them existed.
// All keys are removed under one lock, so the operation is atomic.
func (s *Store) Del(keys ...string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	n := 0
	for _, key := range keys {
		if _, ok := s.data[key]; ok {
			delete(s.data, key)
			n++
		}
	}
	return n
}

// Exists returns how many of the given keys exist.
// A key passed twice is counted twice, same as in Redis.
func (s *Store) Exists(keys ...string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	n := 0
	for _, key := range keys {
		if _, ok := s.data[key]; ok {
			n++
		}
	}
	return n
}
