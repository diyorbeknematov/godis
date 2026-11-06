package store

import (
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestSetGet(t *testing.T) {
	s := New()

	if _, ok := s.Get("ism"); ok {
		t.Fatal("Get on empty store: ok = true, want false")
	}

	s.Set("ism", "Ali")
	if got, ok := s.Get("ism"); !ok || got != "Ali" {
		t.Errorf("Get = %q, %v; want %q, true", got, ok, "Ali")
	}

	// Set overwrites the old value
	s.Set("ism", "Vali")
	if got, _ := s.Get("ism"); got != "Vali" {
		t.Errorf("after overwrite: Get = %q, want %q", got, "Vali")
	}
}

// An empty value is a real value, not a missing key
func TestGetEmptyValue(t *testing.T) {
	s := New()
	s.Set("bosh", "")

	if got, ok := s.Get("bosh"); !ok || got != "" {
		t.Errorf("Get = %q, %v; want \"\", true", got, ok)
	}
}

func TestDel(t *testing.T) {
	s := New()
	s.Set("a", "1")
	s.Set("b", "2")

	if n := s.Del("a", "b", "c"); n != 2 {
		t.Errorf("Del = %d, want 2", n)
	}
	if n := s.Exists("a", "b"); n != 0 {
		t.Errorf("after Del: Exists = %d, want 0", n)
	}
	if n := s.Del("a"); n != 0 {
		t.Errorf("Del of missing key = %d, want 0", n)
	}
}

// Unlike Exists, Del counts a repeated key once: the second time it is already gone
func TestDelDuplicateKeys(t *testing.T) {
	s := New()
	s.Set("a", "1")

	if n := s.Del("a", "a"); n != 1 {
		t.Errorf("Del(a, a) = %d, want 1", n)
	}
}

func TestExists(t *testing.T) {
	s := New()
	s.Set("a", "1")

	tests := []struct {
		name string
		keys []string
		want int
	}{
		{name: "one existing", keys: []string{"a"}, want: 1},
		{name: "one missing", keys: []string{"x"}, want: 0},
		{name: "mixed", keys: []string{"a", "x"}, want: 1},
		{name: "same key twice", keys: []string{"a", "a"}, want: 2},
		{name: "no keys", keys: nil, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := s.Exists(tt.keys...); got != tt.want {
				t.Errorf("Exists(%q) = %d, want %d", tt.keys, got, tt.want)
			}
		})
	}
}

// Run with -race: without the mutex this test reports a data race
func TestConcurrentAccess(t *testing.T) {
	s := New()
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := strconv.Itoa(i % 10)
			s.Set(key, "v")
			s.Get(key)
			s.Exists(key)
			s.Del(key)
		}(i)
	}
	wg.Wait()
}

// newTestStore returns a store with a fake clock and a function to move it forward.
// The clock has its own mutex: a background goroutine may read it while the test advances it.
func newTestStore() (*Store, func(time.Duration)) {
	var mu sync.Mutex
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	s := New()
	s.now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	return s, func(d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		now = now.Add(d)
	}
}

func TestSetWithTTL(t *testing.T) {
	s, advance := newTestStore()
	s.SetWithTTL("ism", "Ali", 10*time.Second)

	advance(9 * time.Second)
	if got, ok := s.Get("ism"); !ok || got != "Ali" {
		t.Fatalf("before deadline: Get = %q, %v; want %q, true", got, ok, "Ali")
	}

	// Exactly at the deadline the key is already gone
	advance(1 * time.Second)
	if _, ok := s.Get("ism"); ok {
		t.Fatal("at deadline: Get ok = true, want false")
	}
	if n := s.Exists("ism"); n != 0 {
		t.Errorf("at deadline: Exists = %d, want 0", n)
	}
	// Get removed the expired entry from the map
	if _, ok := s.data["ism"]; ok {
		t.Error("expired key is still in the map")
	}
}

func TestSetClearsTTL(t *testing.T) {
	s, advance := newTestStore()
	s.SetWithTTL("ism", "Ali", time.Second)
	s.Set("ism", "Vali")

	advance(time.Hour)
	if got, ok := s.Get("ism"); !ok || got != "Vali" {
		t.Errorf("Get = %q, %v; want %q, true", got, ok, "Vali")
	}
}

func TestExpire(t *testing.T) {
	s, advance := newTestStore()

	if s.Expire("yoq", time.Second) {
		t.Error("Expire on missing key = true, want false")
	}

	s.Set("ism", "Ali")
	if !s.Expire("ism", 5*time.Second) {
		t.Fatal("Expire on existing key = false, want true")
	}
	advance(5 * time.Second)
	if _, ok := s.Get("ism"); ok {
		t.Error("after Expire deadline: Get ok = true, want false")
	}

	// Non-positive ttl deletes the key immediately
	s.Set("ism", "Ali")
	if !s.Expire("ism", 0) {
		t.Fatal("Expire(0) on existing key = false, want true")
	}
	if n := s.Exists("ism"); n != 0 {
		t.Errorf("after Expire(0): Exists = %d, want 0", n)
	}
}

func TestTTL(t *testing.T) {
	s, advance := newTestStore()

	if got := s.TTL("yoq"); got != KeyMissing {
		t.Errorf("missing key: TTL = %v, want KeyMissing", got)
	}

	s.Set("doimiy", "1")
	if got := s.TTL("doimiy"); got != NoExpiry {
		t.Errorf("no expiry: TTL = %v, want NoExpiry", got)
	}

	s.SetWithTTL("vaqtli", "1", 10*time.Second)
	advance(3 * time.Second)
	if got := s.TTL("vaqtli"); got != 7*time.Second {
		t.Errorf("with expiry: TTL = %v, want 7s", got)
	}

	advance(7 * time.Second)
	if got := s.TTL("vaqtli"); got != KeyMissing {
		t.Errorf("expired: TTL = %v, want KeyMissing", got)
	}
}

func TestPersist(t *testing.T) {
	s, advance := newTestStore()

	if s.Persist("yoq") {
		t.Error("Persist on missing key = true, want false")
	}

	s.Set("doimiy", "1")
	if s.Persist("doimiy") {
		t.Error("Persist on key without expiry = true, want false")
	}

	s.SetWithTTL("vaqtli", "1", time.Second)
	if !s.Persist("vaqtli") {
		t.Fatal("Persist on key with expiry = false, want true")
	}
	advance(time.Hour)
	if got := s.TTL("vaqtli"); got != NoExpiry {
		t.Errorf("after Persist: TTL = %v, want NoExpiry", got)
	}
}

// Expired keys count as missing for Del, and still get removed
func TestDelExpired(t *testing.T) {
	s, advance := newTestStore()
	s.SetWithTTL("a", "1", time.Second)
	s.Set("b", "2")

	advance(time.Second)
	if n := s.Del("a", "b"); n != 1 {
		t.Errorf("Del = %d, want 1", n)
	}
	if len(s.data) != 0 {
		t.Errorf("map still has %d entries, want 0", len(s.data))
	}
}

// Consistent with Expire: a non-positive ttl leaves no key behind
func TestSetWithTTLNonPositive(t *testing.T) {
	s, _ := newTestStore()
	s.Set("ism", "Ali")

	s.SetWithTTL("ism", "Vali", 0)
	if n := s.Exists("ism"); n != 0 {
		t.Errorf("after SetWithTTL(0): Exists = %d, want 0", n)
	}
	if len(s.data) != 0 {
		t.Errorf("map still has %d entries, want 0", len(s.data))
	}
}
