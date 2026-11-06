package store

import (
	"strconv"
	"sync"
	"testing"
	"time"
)

// size returns the number of entries in the map, dead or alive.
func (s *Store) size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.data)
}

func TestDeleteExpired(t *testing.T) {
	s, advance := newTestStore()
	for i := 0; i < 10; i++ {
		s.SetWithTTL("olik"+strconv.Itoa(i), "v", time.Second)
		s.SetWithTTL("tirik"+strconv.Itoa(i), "v", time.Hour)
		s.Set("doimiy"+strconv.Itoa(i), "v")
	}

	// Nothing has expired yet
	if n := s.DeleteExpired(); n != 0 {
		t.Fatalf("before deadline: DeleteExpired = %d, want 0", n)
	}

	advance(time.Second)
	// Only 20 keys have a timeout, so the first round checks all of them and
	// deletes the 10 dead ones. That is more than a quarter, so a second round
	// runs, finds nothing dead and stops
	if n := s.DeleteExpired(); n != 10 {
		t.Fatalf("DeleteExpired = %d, want 10", n)
	}
	if got := s.size(); got != 20 {
		t.Errorf("size = %d, want 20 (alive + without timeout)", got)
	}
	for i := 0; i < 10; i++ {
		if _, ok := s.data["olik"+strconv.Itoa(i)]; ok {
			t.Errorf("dead key olik%d is still in the map", i)
		}
	}
}

// Many dead keys: one call removes more than one sample, but stops at the round cap
func TestDeleteExpiredRoundLimit(t *testing.T) {
	s, advance := newTestStore()
	const dead = expireSample * expireMaxRounds * 2
	for i := 0; i < dead; i++ {
		s.SetWithTTL(strconv.Itoa(i), "v", time.Second)
	}
	advance(time.Second)

	if n := s.DeleteExpired(); n != expireSample*expireMaxRounds {
		t.Fatalf("DeleteExpired = %d, want %d", n, expireSample*expireMaxRounds)
	}
	// The next calls (next ticks in the server) finish the job
	s.DeleteExpired()
	if got := s.size(); got != 0 {
		t.Errorf("after second call: size = %d, want 0", got)
	}
}

func TestDeleteExpiredEmpty(t *testing.T) {
	s, _ := newTestStore()
	if n := s.DeleteExpired(); n != 0 {
		t.Errorf("DeleteExpired on empty store = %d, want 0", n)
	}
}

// The background goroutine really deletes keys nobody reads
func TestStartExpiryDeletes(t *testing.T) {
	s, advance := newTestStore()
	s.SetWithTTL("k", "v", time.Second)
	advance(time.Second)

	stop := s.StartExpiry(time.Millisecond)
	defer stop()

	// Poll instead of one Sleep: fast machines finish early, slow ones still have time
	deadline := time.Now().Add(time.Second)
	for s.size() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("expired key was not deleted within 1s")
		}
		time.Sleep(time.Millisecond)
	}
}

// stop waits for the goroutine to exit and may be called from several goroutines at once
func TestStartExpiryStop(t *testing.T) {
	s, advance := newTestStore()
	stop := s.StartExpiry(time.Millisecond)

	// Moving the clock while the goroutine reads it: -race checks the fake clock lock
	for i := 0; i < 10; i++ {
		advance(time.Second)
		time.Sleep(time.Millisecond)
	}

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stop()
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("concurrent stop calls did not return within 1s")
	}
}
