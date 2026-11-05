package store

import (
	"strconv"
	"sync"
	"testing"
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
