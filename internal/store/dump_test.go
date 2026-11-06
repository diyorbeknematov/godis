// An external test package: replaying needs the command package, which
// itself imports store
package store_test

import (
	"reflect"
	"sort"
	"testing"
	"time"

	"godis/internal/command"
	"godis/internal/store"
)

// Replaying Dump into an empty store rebuilds the same data, timeouts included
func TestDumpReplay(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	orig := store.NewWithClock(clock)
	run := func(args ...string) { command.Execute(orig, args) }

	run("SET", "ism", "Ali")
	run("SET", "ism", "Vali") // overwritten: only the last value is dumped
	run("SET", "vaqtli", "x", "EX", "100")
	run("SET", "bosh", "")
	run("RPUSH", "l", "a", "b", "c")
	run("LPOP", "l")
	run("EXPIRE", "l", "50")
	run("HSET", "h", "f1", "v1", "f2", "v2")
	run("SADD", "s", "x", "y", "z")
	run("SREM", "s", "y")
	run("SET", "olik", "x", "PX", "10")
	now = now.Add(time.Second) // "olik" has expired

	dump := orig.Dump()
	for _, cmd := range dump {
		if cmd[1] == "olik" {
			t.Errorf("expired key was dumped: %q", cmd)
		}
	}

	replayed := store.NewWithClock(clock)
	for _, cmd := range dump {
		command.Execute(replayed, cmd)
	}

	queries := [][]string{
		{"GET", "ism"}, {"GET", "vaqtli"}, {"PTTL", "vaqtli"}, {"GET", "bosh"},
		{"LRANGE", "l", "0", "-1"}, {"PTTL", "l"},
		{"HGET", "h", "f1"}, {"HLEN", "h"},
		{"SCARD", "s"}, {"SISMEMBER", "s", "y"}, {"EXISTS", "olik"},
		{"TYPE", "l"}, {"TYPE", "h"}, {"TYPE", "s"},
	}
	for _, q := range queries {
		want := command.Execute(orig, q)
		got := command.Execute(replayed, q)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q: replayed %#v, original %#v", q, got, want)
		}
	}
}

// One command per key, whatever the history was
func TestDumpIsCompact(t *testing.T) {
	s := store.New()
	for i := 0; i < 1000; i++ {
		command.Execute(s, []string{"INCR", "n"})
		command.Execute(s, []string{"SADD", "s", "a"})
	}

	dump := s.Dump()
	sort.Slice(dump, func(i, j int) bool { return dump[i][1] < dump[j][1] })
	want := [][]string{{"SET", "n", "1000"}, {"SADD", "s", "a"}}
	if !reflect.DeepEqual(dump, want) {
		t.Errorf("Dump = %q, want %q", dump, want)
	}
}
