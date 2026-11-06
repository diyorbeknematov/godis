package command

import (
	"reflect"
	"strconv"
	"testing"
	"time"

	"godis/internal/resp"
	"godis/internal/store"
)

// What Run hands to the AOF for each command
func TestRunPropagation(t *testing.T) {
	s, _ := newTestStore()
	// Fake clock time in ms, to build the expected absolute deadlines
	nowMs := s.Now().UnixMilli()
	at := func(d time.Duration) string { return strconv.FormatInt(nowMs+d.Milliseconds(), 10) }

	steps := []struct {
		args []string
		want [][]string
	}{
		// Reads and errors are never logged
		{args: []string{"GET", "k"}, want: nil},
		{args: []string{"PING"}, want: nil},
		{args: []string{"SET", "k"}, want: nil},
		{args: []string{"FOO"}, want: nil},

		// Plain writes are logged as sent
		{args: []string{"SET", "k", "v"}, want: [][]string{{"SET", "k", "v"}}},
		{args: []string{"set", "ism", "Ali"}, want: [][]string{{"set", "ism", "Ali"}}},
		{args: []string{"INCR", "ism"}, want: nil}, // a write command, but it failed: "Ali" is not a number
		{args: []string{"INCR", "n"}, want: [][]string{{"INCR", "n"}}},
		{args: []string{"MSET", "a", "1", "b", "2"}, want: [][]string{{"MSET", "a", "1", "b", "2"}}},
		{args: []string{"DEL", "a"}, want: [][]string{{"DEL", "a"}}},

		// Relative timeouts become absolute deadlines
		{args: []string{"SET", "t", "v", "EX", "10"}, want: [][]string{{"SET", "t", "v", "PXAT", at(10 * time.Second)}}},
		{args: []string{"SET", "t", "v", "px", "1500"}, want: [][]string{{"SET", "t", "v", "PXAT", at(1500 * time.Millisecond)}}},
		{args: []string{"SET", "t", "v", "NX", "EX", "5"}, want: nil}, // NX failed: nothing changed
		{args: []string{"SET", "t2", "v", "EX", "5", "NX"}, want: [][]string{{"SET", "t2", "v", "PXAT", at(5 * time.Second), "NX"}}},
		{args: []string{"EXPIRE", "k", "100"}, want: [][]string{{"PEXPIREAT", "k", at(100 * time.Second)}}},
		{args: []string{"EXPIRE", "yoq", "100"}, want: nil}, // key missing: nothing changed
		{args: []string{"SET", "nx", "v", "XX"}, want: nil},
	}

	for i, st := range steps {
		_, got := Run(s, st.args)
		if !reflect.DeepEqual(got, st.want) {
			t.Fatalf("step %d %q: logged %q, want %q", i+1, st.args, got, st.want)
		}
	}
}

// The real point of the AOF: replaying what was logged into an empty store,
// even much later, gives exactly the data the original store had
func TestReplayGivesSameState(t *testing.T) {
	orig, advance := newTestStore()
	var logged [][]string
	run := func(args ...string) {
		_, cmds := Run(orig, args)
		logged = append(logged, cmds...)
	}

	run("SET", "ism", "Ali")
	run("SET", "ism", "Vali")
	run("SET", "vaqtli", "x", "EX", "100")
	run("SET", "tez", "x", "PX", "500")
	run("SET", "doimiy", "x")
	run("EXPIRE", "doimiy", "60")
	run("PERSIST", "doimiy")
	run("SET", "muddatli", "x")
	run("EXPIRE", "muddatli", "60") // replayed later, it must still end at the same moment
	run("INCR", "n")
	run("INCRBY", "n", "41")
	run("APPEND", "ism", "jon")
	run("MSET", "a", "1", "b", "2")
	run("DEL", "a")
	advance(10 * time.Second) // "tez" expires in the original too

	// Replay into a fresh store that reads the same (advanced) clock
	replayed := store.NewWithClock(orig.Now)
	for _, args := range logged {
		if r, _ := Run(replayed, args); reflect.TypeOf(r) == reflect.TypeOf(resp.Error("")) {
			t.Fatalf("replay %q: %#v", args, r)
		}
	}

	for _, key := range []string{"ism", "vaqtli", "tez", "doimiy", "muddatli", "n", "a", "b"} {
		for _, cmd := range []string{"GET", "PTTL"} {
			want := Execute(orig, []string{cmd, key})
			got := Execute(replayed, []string{cmd, key})
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s %s: replayed %#v, original %#v", cmd, key, got, want)
			}
		}
	}
}
