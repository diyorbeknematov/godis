package store

import (
	"sync"
	"time"
)

// Special TTL results, same meaning as Redis' -2 and -1.
// Real remaining durations are always positive, so they never collide,
// as long as one operation reads the clock only once.
const (
	KeyMissing time.Duration = -2
	NoExpiry   time.Duration = -1
)

// entry is a stored value with an optional expiration time.
type entry struct {
	value     string
	expiresAt time.Time // zero means the key never expires
}

// expired reports whether the entry's deadline has passed at now.
func (e entry) expired(now time.Time) bool {
	return !e.expiresAt.IsZero() && !now.Before(e.expiresAt)
}

// Store is an in-memory key-value store, safe for concurrent use.
type Store struct {
	mu   sync.RWMutex
	data map[string]entry
	now  func() time.Time // replaced in tests to control time
}

func New() *Store {
	return NewWithClock(time.Now)
}

// NewWithClock is like New but reads the current time from now.
// Tests in other packages use it to control expiration.
func NewWithClock(now func() time.Time) *Store {
	return &Store{
		data: make(map[string]entry),
		now:  now,
	}
}

// Set stores value under key, overwriting any previous value.
// Like Redis SET, it also clears any existing expiration.
func (s *Store) Set(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data[key] = entry{value: value}
}

// SetWithTTL stores value under key and makes it expire after ttl.
// A non-positive ttl deletes the key instead, same as Expire.
func (s *Store) SetWithTTL(key, value string, ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if ttl <= 0 {
		delete(s.data, key)
		return
	}
	s.data[key] = entry{value: value, expiresAt: s.now().Add(ttl)}
}

// Get returns the value for key. ok is false if the key does not exist
// or has expired, which lets callers tell a missing key from an empty value.
func (s *Store) Get(key string) (value string, ok bool) {
	// Fast path: most keys are not expired, so a read lock is enough
	s.mu.RLock()
	e, ok := s.data[key]
	expired := ok && e.expired(s.now())
	s.mu.RUnlock()

	if !ok {
		return "", false
	}
	if !expired {
		return e.value, true
	}

	// Slow path: an RLock cannot be upgraded, so take a write lock separately.
	// Between RUnlock and Lock another goroutine may have SET the key again,
	// so look it up once more instead of deleting blindly.
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok = s.getLocked(key, s.now())
	return e.value, ok
}

// Del removes the given keys and returns how many of them existed.
// All keys are removed under one lock, so the operation is atomic.
func (s *Store) Del(keys ...string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	n := 0
	for _, key := range keys {
		if _, ok := s.getLocked(key, now); ok {
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

	now := s.now()
	n := 0
	for _, key := range keys {
		// Expired keys are only skipped here; removing them needs a write lock
		if e, ok := s.data[key]; ok && !e.expired(now) {
			n++
		}
	}
	return n
}

// Expire sets a timeout on key and reports whether the key existed.
// A non-positive ttl deletes the key right away, as in Redis.
func (s *Store) Expire(key string, ttl time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	return s.expireAtLocked(key, now.Add(ttl), now)
}

// ExpireAt makes key expire at the absolute time at and reports whether
// the key existed. A time that is not in the future deletes the key.
// The AOF stores deadlines this way: "expire in 10s" would mean something
// else when the file is replayed an hour later.
func (s *Store) ExpireAt(key string, at time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.expireAtLocked(key, at, s.now())
}

func (s *Store) expireAtLocked(key string, at, now time.Time) bool {
	e, ok := s.getLocked(key, now)
	if !ok {
		return false
	}
	if !at.After(now) {
		delete(s.data, key)
		return true
	}
	e.expiresAt = at
	s.data[key] = e
	return true
}

// Now returns the store's current time. Commands use it to turn a relative
// timeout into an absolute deadline with the same clock the store uses.
func (s *Store) Now() time.Time {
	return s.now()
}

// TTL returns the remaining time to live of key,
// KeyMissing if the key does not exist, or NoExpiry if it has no timeout.
func (s *Store) TTL(key string) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()

	// One clock reading for both the expiry check and the subtraction:
	// with two readings a key alive at the first one could give a
	// negative result at the second, e.g. -1ns, which equals NoExpiry
	now := s.now()
	e, ok := s.getLocked(key, now)
	if !ok {
		return KeyMissing
	}
	if e.expiresAt.IsZero() {
		return NoExpiry
	}
	return e.expiresAt.Sub(now)
}

// Persist removes the timeout from key and reports whether it had one.
func (s *Store) Persist(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.getLocked(key, s.now())
	if !ok || e.expiresAt.IsZero() {
		return false
	}
	e.expiresAt = time.Time{}
	s.data[key] = e
	return true
}

// getLocked returns the live entry for key at now and deletes it if it has expired.
// The caller must hold the write lock. now is passed in so that one operation
// uses a single clock reading everywhere.
func (s *Store) getLocked(key string, now time.Time) (entry, bool) {
	e, ok := s.data[key]
	if !ok {
		return entry{}, false
	}
	if e.expired(now) {
		delete(s.data, key)
		return entry{}, false
	}
	return e, true
}
