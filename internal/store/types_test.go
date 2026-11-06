package store

import (
	"errors"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"
)

func TestType(t *testing.T) {
	s, advance := newTestStore()
	s.Set("str", "v")
	s.RPush("list", "a")
	s.HSet("hash", "f", "v")
	s.SAdd("set", "m")
	s.SetWithTTL("expired", "v", time.Second)
	advance(time.Second)

	for key, want := range map[string]string{
		"str": "string", "list": "list", "hash": "hash", "set": "set",
		"yoq": "none", "expired": "none",
	} {
		if got := s.Type(key); got != want {
			t.Errorf("Type(%s) = %q, want %q", key, got, want)
		}
	}
}

// Every type-specific method on a key of another type fails with ErrWrongType
// and leaves the key as it was
func TestWrongType(t *testing.T) {
	s, _ := newTestStore()
	s.Set("str", "v")
	s.RPush("list", "a")
	s.HSet("hash", "f", "v")
	s.SAdd("set", "m")

	calls := map[string]func() error{
		"Get on list":       func() error { _, _, err := s.Get("list"); return err },
		"IncrBy on hash":    func() error { _, err := s.IncrBy("hash", 1); return err },
		"Append on set":     func() error { _, err := s.Append("set", "x"); return err },
		"Strlen on list":    func() error { _, err := s.Strlen("list"); return err },
		"LPush on string":   func() error { _, err := s.LPush("str", "x"); return err },
		"RPop on hash":      func() error { _, _, err := s.RPop("hash"); return err },
		"LLen on set":       func() error { _, err := s.LLen("set"); return err },
		"LRange on string":  func() error { _, err := s.LRange("str", 0, -1); return err },
		"HSet on list":      func() error { _, err := s.HSet("list", "f", "v"); return err },
		"HGet on string":    func() error { _, _, err := s.HGet("str", "f"); return err },
		"HDel on set":       func() error { _, err := s.HDel("set", "f"); return err },
		"HGetAll on list":   func() error { _, err := s.HGetAll("list"); return err },
		"SAdd on hash":      func() error { _, err := s.SAdd("hash", "m"); return err },
		"SRem on string":    func() error { _, err := s.SRem("str", "m"); return err },
		"SIsMember on list": func() error { _, err := s.SIsMember("list", "m"); return err },
		"SMembers on hash":  func() error { _, err := s.SMembers("hash"); return err },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, ErrWrongType) {
			t.Errorf("%s: error = %v, want ErrWrongType", name, err)
		}
	}

	for key, want := range map[string]string{"str": "string", "list": "list", "hash": "hash", "set": "set"} {
		if got := s.Type(key); got != want {
			t.Errorf("after wrong-type calls: Type(%s) = %q, want %q", key, got, want)
		}
	}
	// MGET does not fail: a key of another type is just nil
	if v := s.MGet("list"); v[0] != nil {
		t.Errorf("MGet on list = %q, want nil", *v[0])
	}
}

// SET replaces a key of any type, as in Redis
func TestSetOverwritesOtherTypes(t *testing.T) {
	s, _ := newTestStore()
	s.RPush("k", "a")
	s.Set("k", "v")
	if got := s.Type("k"); got != "string" {
		t.Errorf("Type after SET = %q, want string", got)
	}
}

func TestList(t *testing.T) {
	s, _ := newTestStore()

	// LPUSH inserts one by one at the head, so the values end up reversed
	if n, _ := s.LPush("l", "a", "b", "c"); n != 3 {
		t.Fatalf("LPush = %d, want 3", n)
	}
	if n, _ := s.RPush("l", "x", "y"); n != 5 {
		t.Fatalf("RPush = %d, want 5", n)
	}
	if got, _ := s.LRange("l", 0, -1); !reflect.DeepEqual(got, []string{"c", "b", "a", "x", "y"}) {
		t.Fatalf("LRange = %q", got)
	}

	if v, ok, _ := s.LPop("l"); !ok || v != "c" {
		t.Errorf("LPop = %q, %v; want c", v, ok)
	}
	if v, ok, _ := s.RPop("l"); !ok || v != "y" {
		t.Errorf("RPop = %q, %v; want y", v, ok)
	}
	if n, _ := s.LLen("l"); n != 3 {
		t.Errorf("LLen = %d, want 3", n)
	}
}

func TestLRangeIndexes(t *testing.T) {
	s, _ := newTestStore()
	s.RPush("l", "a", "b", "c", "d", "e")

	tests := []struct {
		start, stop int
		want        []string
	}{
		{0, -1, []string{"a", "b", "c", "d", "e"}},
		{1, 2, []string{"b", "c"}},
		{-2, -1, []string{"d", "e"}},
		{0, 0, []string{"a"}},
		{-100, 1, []string{"a", "b"}}, // start clamped to 0
		{3, 100, []string{"d", "e"}},  // stop clamped to the end
		{3, 1, []string{}},            // start after stop
		{5, 10, []string{}},           // start past the end
		{-1, -2, []string{}},
	}
	for _, tt := range tests {
		got, err := s.LRange("l", tt.start, tt.stop)
		if err != nil || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("LRange(%d, %d) = %q, %v; want %q", tt.start, tt.stop, got, err, tt.want)
		}
	}

	if got, _ := s.LRange("yoq", 0, -1); len(got) != 0 {
		t.Errorf("LRange on missing key = %q, want empty", got)
	}
}

// The slice LRange returns is a copy: changing it must not change the list
func TestLRangeReturnsCopy(t *testing.T) {
	s, _ := newTestStore()
	s.RPush("l", "a", "b")

	got, _ := s.LRange("l", 0, -1)
	got[0] = "BUZILDI"

	if again, _ := s.LRange("l", 0, 0); again[0] != "a" {
		t.Errorf("list changed through the returned slice: %q", again)
	}
}

// Redis has no empty containers: removing the last element removes the key
func TestEmptyContainerDeletesKey(t *testing.T) {
	s, _ := newTestStore()
	s.RPush("l", "a")
	s.HSet("h", "f", "v")
	s.SAdd("s", "m")

	s.LPop("l")
	s.HDel("h", "f")
	s.SRem("s", "m")

	if n := s.Exists("l", "h", "s"); n != 0 {
		t.Errorf("Exists after emptying = %d, want 0", n)
	}
	if got := s.size(); got != 0 {
		t.Errorf("map still has %d entries", got)
	}
	// Popping a missing list is not an error
	if _, ok, err := s.LPop("l"); ok || err != nil {
		t.Errorf("LPop on missing key = %v, %v; want false, nil", ok, err)
	}
}

func TestHash(t *testing.T) {
	s, _ := newTestStore()

	if n, _ := s.HSet("u", "ism", "Ali", "yosh", "20"); n != 2 {
		t.Fatalf("HSet = %d, want 2 new fields", n)
	}
	// One updated, one new
	if n, _ := s.HSet("u", "yosh", "21", "shahar", "Toshkent"); n != 1 {
		t.Fatalf("second HSet = %d, want 1 new field", n)
	}
	if v, ok, _ := s.HGet("u", "yosh"); !ok || v != "21" {
		t.Errorf("HGet(yosh) = %q, %v; want 21", v, ok)
	}
	if _, ok, _ := s.HGet("u", "yoq"); ok {
		t.Error("HGet of missing field: ok = true")
	}
	if n, _ := s.HLen("u"); n != 3 {
		t.Errorf("HLen = %d, want 3", n)
	}

	pairs, _ := s.HGetAll("u")
	got := map[string]string{}
	for i := 0; i < len(pairs); i += 2 {
		got[pairs[i]] = pairs[i+1]
	}
	want := map[string]string{"ism": "Ali", "yosh": "21", "shahar": "Toshkent"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("HGetAll = %v, want %v", got, want)
	}

	if n, _ := s.HDel("u", "ism", "yoq"); n != 1 {
		t.Errorf("HDel = %d, want 1", n)
	}
}

func TestSet(t *testing.T) {
	s, _ := newTestStore()

	if n, _ := s.SAdd("s", "a", "b", "a"); n != 2 {
		t.Fatalf("SAdd with a duplicate = %d, want 2", n)
	}
	if n, _ := s.SAdd("s", "b", "c"); n != 1 {
		t.Fatalf("second SAdd = %d, want 1", n)
	}
	if ok, _ := s.SIsMember("s", "c"); !ok {
		t.Error("SIsMember(c) = false")
	}
	if ok, _ := s.SIsMember("s", "z"); ok {
		t.Error("SIsMember(z) = true")
	}
	if n, _ := s.SCard("s"); n != 3 {
		t.Errorf("SCard = %d, want 3", n)
	}

	members, _ := s.SMembers("s")
	sort.Strings(members)
	if !reflect.DeepEqual(members, []string{"a", "b", "c"}) {
		t.Errorf("SMembers = %q", members)
	}

	if n, _ := s.SRem("s", "a", "z"); n != 1 {
		t.Errorf("SRem = %d, want 1", n)
	}
}

// Containers expire like strings, and a new push after expiry starts fresh
func TestContainerExpires(t *testing.T) {
	s, advance := newTestStore()
	s.RPush("l", "eski")
	s.Expire("l", time.Second)
	advance(time.Second)

	if n, _ := s.LLen("l"); n != 0 {
		t.Errorf("LLen of expired list = %d, want 0", n)
	}
	s.RPush("l", "yangi")
	if got, _ := s.LRange("l", 0, -1); !reflect.DeepEqual(got, []string{"yangi"}) {
		t.Errorf("after expiry and push: %q, want [yangi]", got)
	}
	if got := s.TTL("l"); got != NoExpiry {
		t.Errorf("TTL of new list = %v, want NoExpiry", got)
	}
}

// Concurrent writers and readers on one list, hash and set: -race checks the
// locking, the final counts check that no write was lost
func TestContainersConcurrent(t *testing.T) {
	s := New()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v := string(rune('A' + i))
			s.RPush("l", v)
			s.HSet("h", v, v)
			s.SAdd("s", v)
			s.LRange("l", 0, -1)
			s.HGetAll("h")
			s.SMembers("s")
		}(i)
	}
	wg.Wait()

	n1, _ := s.LLen("l")
	n2, _ := s.HLen("h")
	n3, _ := s.SCard("s")
	if n1 != 50 || n2 != 50 || n3 != 50 {
		t.Errorf("LLen, HLen, SCard = %d, %d, %d; want 50 each", n1, n2, n3)
	}
}
