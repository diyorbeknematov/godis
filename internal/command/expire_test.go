package command

import (
	"reflect"
	"sync"
	"testing"
	"time"

	"godis/internal/resp"
	"godis/internal/store"
)

// newTestStore returns a store with a fake clock and a function to move it forward.
// The clock has its own mutex so it stays safe if a background goroutine reads it.
func newTestStore() (*store.Store, func(time.Duration)) {
	var mu sync.Mutex
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	s := store.NewWithClock(func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	})
	return s, func(d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		now = now.Add(d)
	}
}

var (
	invalidSet       = resp.Error("ERR invalid expire time in 'set' command")
	invalidExpireCmd = resp.Error("ERR invalid expire time in 'expire' command")
)

func TestSetOptions(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want resp.Reply
	}{
		{name: "EX", args: []string{"SET", "k", "v", "EX", "10"}, want: resp.SimpleString("OK")},
		{name: "PX", args: []string{"SET", "k", "v", "PX", "500"}, want: resp.SimpleString("OK")},
		{name: "lowercase ex", args: []string{"SET", "k", "v", "ex", "10"}, want: resp.SimpleString("OK")},
		{name: "mixed case Px", args: []string{"SET", "k", "v", "Px", "10"}, want: resp.SimpleString("OK")},
		{name: "EX and PX", args: []string{"SET", "k", "v", "EX", "10", "PX", "100"}, want: errSyntax},
		{name: "EX twice", args: []string{"SET", "k", "v", "EX", "10", "EX", "5"}, want: errSyntax},
		{name: "EX without value", args: []string{"SET", "k", "v", "EX"}, want: errSyntax},
		{name: "unknown option", args: []string{"SET", "k", "v", "foo"}, want: errSyntax},
		{name: "EX not a number", args: []string{"SET", "k", "v", "EX", "abc"}, want: errNotInteger},
		{name: "EX zero", args: []string{"SET", "k", "v", "EX", "0"}, want: invalidSet},
		{name: "EX negative", args: []string{"SET", "k", "v", "EX", "-5"}, want: invalidSet},
		{name: "EX overflows Duration", args: []string{"SET", "k", "v", "EX", "99999999999"}, want: invalidSet},
		{name: "EX overflows int64", args: []string{"SET", "k", "v", "EX", "99999999999999999999"}, want: errNotInteger},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newTestStore()
			if got := Execute(s, tt.args); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %#v, want %#v", got, tt.want)
			}
			// A rejected SET must not write anything
			if _, isErr := tt.want.(resp.Error); isErr && s.Exists("k") != 0 {
				t.Error("key was written despite the error")
			}
		})
	}
}

// Commands run in order against one store, with the clock moved between steps
func TestExpireCommands(t *testing.T) {
	s, advance := newTestStore()

	steps := []struct {
		advance time.Duration // applied before the command
		args    []string
		want    resp.Reply
	}{
		{args: []string{"TTL", "k"}, want: resp.Integer(-2)},
		{args: []string{"PTTL", "k"}, want: resp.Integer(-2)},
		{args: []string{"SET", "k", "v"}, want: resp.SimpleString("OK")},
		{args: []string{"TTL", "k"}, want: resp.Integer(-1)},
		{args: []string{"PTTL", "k"}, want: resp.Integer(-1)},

		{args: []string{"EXPIRE", "k", "10"}, want: resp.Integer(1)},
		{args: []string{"TTL", "k"}, want: resp.Integer(10)},
		// 9.6s left: rounded up to 10, not truncated to 9
		{advance: 400 * time.Millisecond, args: []string{"TTL", "k"}, want: resp.Integer(10)},
		{args: []string{"PTTL", "k"}, want: resp.Integer(9600)},
		// 9.4s left: rounded down to 9
		{advance: 200 * time.Millisecond, args: []string{"TTL", "k"}, want: resp.Integer(9)},

		{args: []string{"PERSIST", "k"}, want: resp.Integer(1)},
		{args: []string{"PERSIST", "k"}, want: resp.Integer(0)},
		{args: []string{"TTL", "k"}, want: resp.Integer(-1)},

		{args: []string{"SET", "k", "v", "PX", "1500"}, want: resp.SimpleString("OK")},
		{args: []string{"PTTL", "k"}, want: resp.Integer(1500)},
		{advance: 1500 * time.Millisecond, args: []string{"GET", "k"}, want: resp.Null{}},
		{args: []string{"TTL", "k"}, want: resp.Integer(-2)},

		{args: []string{"EXPIRE", "yoq", "10"}, want: resp.Integer(0)},
		{args: []string{"PERSIST", "yoq"}, want: resp.Integer(0)},

		// Non-positive EXPIRE deletes the key right away
		{args: []string{"SET", "k", "v"}, want: resp.SimpleString("OK")},
		{args: []string{"EXPIRE", "k", "0"}, want: resp.Integer(1)},
		{args: []string{"EXISTS", "k"}, want: resp.Integer(0)},

		// Errors
		{args: []string{"EXPIRE", "k", "abc"}, want: errNotInteger},
		{args: []string{"EXPIRE", "k", "99999999999"}, want: invalidExpireCmd},
		{args: []string{"EXPIRE", "k"}, want: resp.Error("ERR wrong number of arguments for 'expire' command")},
		{args: []string{"TTL"}, want: resp.Error("ERR wrong number of arguments for 'ttl' command")},
		{args: []string{"PERSIST", "a", "b"}, want: resp.Error("ERR wrong number of arguments for 'persist' command")},
	}

	// Steps depend on each other, so stop at the first failure: it is the real cause
	for i, st := range steps {
		advance(st.advance)
		if got := Execute(s, st.args); !reflect.DeepEqual(got, st.want) {
			t.Fatalf("step %d %q: got %#v, want %#v", i+1, st.args, got, st.want)
		}
	}
}
