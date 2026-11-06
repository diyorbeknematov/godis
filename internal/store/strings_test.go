package store

import (
	"errors"
	"math"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestIncrBy(t *testing.T) {
	s, _ := newTestStore()

	// A missing key counts as 0
	if n, err := s.IncrBy("n", 5); err != nil || n != 5 {
		t.Fatalf("IncrBy on missing key = %d, %v; want 5, nil", n, err)
	}
	if n, err := s.IncrBy("n", -7); err != nil || n != -2 {
		t.Fatalf("IncrBy(-7) = %d, %v; want -2, nil", n, err)
	}
	if got, _, _ := s.Get("n"); got != "-2" {
		t.Errorf("stored value = %q, want \"-2\"", got)
	}
}

func TestIncrByErrors(t *testing.T) {
	tests := []struct {
		name  string
		value string
		delta int64
		want  error
	}{
		{name: "not a number", value: "Ali", delta: 1, want: ErrNotInteger},
		{name: "float", value: "1.5", delta: 1, want: ErrNotInteger},
		{name: "empty string", value: "", delta: 1, want: ErrNotInteger},
		{name: "over max", value: strconv.FormatInt(math.MaxInt64, 10), delta: 1, want: ErrOverflow},
		{name: "under min", value: strconv.FormatInt(math.MinInt64, 10), delta: -1, want: ErrOverflow},
		{name: "big delta", value: "1", delta: math.MaxInt64, want: ErrOverflow},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newTestStore()
			s.Set("k", tt.value)

			if _, err := s.IncrBy("k", tt.delta); !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			// A failed increment leaves the value as it was
			if got, _, _ := s.Get("k"); got != tt.value {
				t.Errorf("value changed to %q, want %q", got, tt.value)
			}
		})
	}
}

// Many clients incrementing at once must not lose a single update.
// -race cannot catch a lost update, only the final count can.
func TestIncrByConcurrent(t *testing.T) {
	s := New()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.IncrBy("n", 1)
		}()
	}
	wg.Wait()

	if got, _, _ := s.Get("n"); got != "100" {
		t.Errorf("after 100 increments: %q, want \"100\"", got)
	}
}

// INCR and APPEND change the value but keep the timeout, unlike SET
func TestIncrByAppendKeepTTL(t *testing.T) {
	s, advance := newTestStore()
	s.SetWithTTL("n", "1", 10*time.Second)
	s.SetWithTTL("str", "a", 10*time.Second)

	s.IncrBy("n", 1)
	s.Append("str", "b")
	advance(3 * time.Second)

	for _, key := range []string{"n", "str"} {
		if got := s.TTL(key); got != 7*time.Second {
			t.Errorf("TTL(%s) = %v, want 7s", key, got)
		}
	}
}

// An expired key is gone: INCR starts again from 0, not from the old value
func TestIncrByExpired(t *testing.T) {
	s, advance := newTestStore()
	s.SetWithTTL("n", "41", time.Second)
	advance(time.Second)

	if n, _ := s.IncrBy("n", 1); n != 1 {
		t.Errorf("IncrBy on expired key = %d, want 1", n)
	}
	if got := s.TTL("n"); got != NoExpiry {
		t.Errorf("new key TTL = %v, want NoExpiry", got)
	}
}

func TestAppendStrlen(t *testing.T) {
	s, _ := newTestStore()

	if n, _ := s.Strlen("yoq"); n != 0 {
		t.Errorf("Strlen of missing key = %d, want 0", n)
	}
	if n, _ := s.Append("k", "salom"); n != 5 {
		t.Errorf("Append to missing key = %d, want 5", n)
	}
	if n, _ := s.Append("k", " dunyo"); n != 11 {
		t.Errorf("second Append = %d, want 11", n)
	}
	if got, _, _ := s.Get("k"); got != "salom dunyo" {
		t.Errorf("value = %q, want \"salom dunyo\"", got)
	}
	// Length is in bytes, not letters: "ʻ" is 2 bytes in UTF-8
	s.Set("u", "oʻ")
	if n, _ := s.Strlen("u"); n != 3 {
		t.Errorf("Strlen(\"oʻ\") = %d, want 3", n)
	}
}

func TestMSetMGet(t *testing.T) {
	s, advance := newTestStore()
	s.SetWithTTL("eski", "1", time.Second)

	s.MSet("a", "1", "b", "2", "a", "3") // a repeated key: the last value wins
	s.SetWithTTL("vaqtli", "x", time.Second)
	advance(time.Second)

	got := s.MGet("a", "b", "yoq", "vaqtli", "a")
	want := []string{"3", "2", "", "", "3"}
	missing := []bool{false, false, true, true, false}

	if len(got) != len(want) {
		t.Fatalf("MGet returned %d values, want %d", len(got), len(want))
	}
	for i := range got {
		switch {
		case missing[i] && got[i] != nil:
			t.Errorf("value %d = %q, want nil", i, *got[i])
		case !missing[i] && (got[i] == nil || *got[i] != want[i]):
			t.Errorf("value %d = %v, want %q", i, got[i], want[i])
		}
	}
}

// MSet clears an existing timeout, like SET
func TestMSetClearsTTL(t *testing.T) {
	s, _ := newTestStore()
	s.SetWithTTL("a", "1", time.Second)
	s.MSet("a", "2")

	if got := s.TTL("a"); got != NoExpiry {
		t.Errorf("TTL after MSet = %v, want NoExpiry", got)
	}
}

func TestSetIf(t *testing.T) {
	s, advance := newTestStore()

	if s.SetIf("k", "1", time.Time{}, IfExists) {
		t.Error("XX on missing key wrote")
	}
	if !s.SetIf("k", "1", time.Time{}, IfAbsent) {
		t.Error("NX on missing key did not write")
	}
	if s.SetIf("k", "2", time.Time{}, IfAbsent) {
		t.Error("NX on existing key wrote")
	}
	if !s.SetIf("k", "3", time.Time{}, IfExists) {
		t.Error("XX on existing key did not write")
	}
	if got, _, _ := s.Get("k"); got != "3" {
		t.Errorf("value = %q, want \"3\"", got)
	}

	// An expired key counts as missing for NX
	s.SetIf("t", "old", s.Now().Add(time.Second), Always)
	advance(time.Second)
	if !s.SetIf("t", "new", time.Time{}, IfAbsent) {
		t.Error("NX on expired key did not write")
	}
}

// Of many clients racing SET NX on one key, exactly one wins.
// This is what makes SET NX usable as a lock.
func TestSetIfAbsentConcurrent(t *testing.T) {
	s := New()
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if s.SetIf("lock", strconv.Itoa(i), time.Time{}, IfAbsent) {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()

	if wins != 1 {
		t.Errorf("%d clients won SET NX, want exactly 1", wins)
	}
}
