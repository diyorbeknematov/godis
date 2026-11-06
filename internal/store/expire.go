package store

import (
	"sync"
	"time"
)

const (
	// expireSample is how many keys with a timeout one round checks.
	expireSample = 20
	// expireMaxVisit caps how many entries one round may walk past while
	// looking for keys with a timeout, so keys without one cannot make it scan the whole map.
	expireMaxVisit = expireSample * 10
	// expireMaxRounds caps one DeleteExpired call. Every round holds the
	// write lock, so without a cap a store full of dead keys would block clients.
	expireMaxRounds = 16
)

// DeleteExpired removes expired keys that nobody has read, Redis-style:
// it checks a random sample and repeats while more than a quarter of the
// sample was dead, up to expireMaxRounds. It returns how many keys it removed.
//
// It does no waiting and starts no goroutine, so tests can call it directly.
func (s *Store) DeleteExpired() int {
	total := 0
	for round := 0; round < expireMaxRounds; round++ {
		deleted, checked := s.expireRound()
		total += deleted
		if deleted*4 <= checked {
			break
		}
	}
	return total
}

// expireRound checks up to expireSample keys with a timeout and deletes the dead ones.
//
// Map iteration order in Go is random, so the first keys range gives are a
// random sample. Limitation: keys without a timeout are skipped but still
// walked past; with many of them few keys with a timeout get checked. Redis
// avoids this by keeping keys with a timeout in a separate table.
func (s *Store) expireRound() (deleted, checked int) {
	// The lock is taken per round, so clients can run between rounds
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	visited := 0
	for key, e := range s.data {
		if checked == expireSample || visited == expireMaxVisit {
			break
		}
		visited++

		if e.expiresAt.IsZero() {
			continue
		}
		checked++
		if e.expired(now) {
			// Deleting during range is allowed in Go
			delete(s.data, key)
			deleted++
		}
	}
	return deleted, checked
}

// StartExpiry runs DeleteExpired every interval in a background goroutine.
// The returned stop function ends it and waits until it has exited;
// calling stop more than once, even concurrently, is safe.
//
// It is separate from New so that stores in tests do not start goroutines.
func (s *Store) StartExpiry(interval time.Duration) (stop func()) {
	quit := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				s.DeleteExpired()
			case <-quit:
				return
			}
		}
	}()

	var once sync.Once
	return func() {
		// Closing a closed channel panics, so close only once
		once.Do(func() { close(quit) })
		<-done
	}
}
