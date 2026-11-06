package store

import (
	"errors"
	"math"
	"strconv"
	"time"
)

var (
	ErrNotInteger = errors.New("store: value is not an integer")
	ErrOverflow   = errors.New("store: increment or decrement would overflow")
)

// Condition decides whether SetIf writes, based on whether the key exists.
type Condition int

const (
	Always   Condition = iota
	IfAbsent           // SET ... NX
	IfExists           // SET ... XX
)

// SetIf stores value under key if cond holds and reports whether it wrote.
// A non-zero expiresAt makes the key expire at that time; zero means no
// expiration. Like SET, it clears any previous expiration.
// The existence check and the write happen under one lock, so two clients
// running SET NX on the same key can never both succeed.
func (s *Store) SetIf(key, value string, expiresAt time.Time, cond Condition) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, exists := s.getLocked(key, s.now())
	if (cond == IfAbsent && exists) || (cond == IfExists && !exists) {
		return false
	}

	s.data[key] = entry{value: value, expiresAt: expiresAt}
	return true
}

// IncrBy adds delta to the integer stored at key and returns the new value.
// A missing key counts as 0. The key keeps its expiration.
//
// Reading, adding and writing happen under one lock. Doing Get then Set in
// the command handler would lose updates: two clients could both read 5 and
// both write 6. That is not a data race, so -race would not catch it.
func (s *Store) IncrBy(key string, delta int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.getLocked(key, s.now())
	var n int64
	if ok {
		var err error
		if n, err = strconv.ParseInt(e.value, 10, 64); err != nil {
			return 0, ErrNotInteger
		}
	}

	// Check before adding: after the addition an overflow has already wrapped around
	if (delta > 0 && n > math.MaxInt64-delta) || (delta < 0 && n < math.MinInt64-delta) {
		return 0, ErrOverflow
	}
	n += delta

	e.value = strconv.FormatInt(n, 10)
	s.data[key] = e
	return n, nil
}

// Append adds value to the end of the string at key and returns its new length.
// A missing key is created. The key keeps its expiration.
func (s *Store) Append(key, value string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, _ := s.getLocked(key, s.now())
	e.value += value
	s.data[key] = e
	return len(e.value)
}

// Strlen returns the length of the value at key, or 0 if it does not exist.
func (s *Store) Strlen(key string) int {
	value, _ := s.Get(key)
	return len(value)
}

// MGet returns the values of keys in the same order; nil marks a missing key.
func (s *Store) MGet(keys ...string) []*string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := s.now()
	values := make([]*string, len(keys))
	for i, key := range keys {
		// Expired keys are only skipped here, like in Exists
		if e, ok := s.data[key]; ok && !e.expired(now) {
			v := e.value
			values[i] = &v
		}
	}
	return values
}

// MSet stores key-value pairs given as key1, value1, key2, value2, ...
// All pairs are written under one lock, so no client sees half of them.
// Like SET, it clears expirations. It panics if pairs has odd length:
// that is a bug in the caller, the command layer checks it first.
func (s *Store) MSet(pairs ...string) {
	if len(pairs)%2 != 0 {
		panic("store: MSet needs key-value pairs")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for i := 0; i < len(pairs); i += 2 {
		s.data[pairs[i]] = entry{value: pairs[i+1]}
	}
}
