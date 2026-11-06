package command

import (
	"math"
	"reflect"
	"strconv"
	"testing"
	"time"

	"godis/internal/resp"
)

// Commands run in order against one store, the way a client session would
func TestStringCommands(t *testing.T) {
	s, advance := newTestStore()
	maxInt := strconv.FormatInt(math.MaxInt64, 10)
	minInt := strconv.FormatInt(math.MinInt64, 10)

	steps := []struct {
		advance time.Duration // applied before the command
		args    []string
		want    resp.Reply
	}{
		// INCR family
		{args: []string{"INCR", "n"}, want: resp.Integer(1)},
		{args: []string{"incr", "n"}, want: resp.Integer(2)},
		{args: []string{"INCRBY", "n", "10"}, want: resp.Integer(12)},
		{args: []string{"INCRBY", "n", "-20"}, want: resp.Integer(-8)},
		{args: []string{"DECR", "n"}, want: resp.Integer(-9)},
		{args: []string{"DECRBY", "n", "1"}, want: resp.Integer(-10)},
		{args: []string{"GET", "n"}, want: resp.BulkString("-10")},
		{args: []string{"INCRBY", "n", "abc"}, want: errNotInteger},
		{args: []string{"DECRBY", "n", "1.5"}, want: errNotInteger},
		{args: []string{"DECRBY", "n", minInt}, want: resp.Error("ERR decrement would overflow")},
		{args: []string{"SET", "ism", "Ali"}, want: resp.SimpleString("OK")},
		{args: []string{"INCR", "ism"}, want: errNotInteger},
		{args: []string{"SET", "max", maxInt}, want: resp.SimpleString("OK")},
		{args: []string{"INCR", "max"}, want: errOverflow},
		{args: []string{"GET", "max"}, want: resp.BulkString(maxInt)},
		{args: []string{"INCR"}, want: resp.Error("ERR wrong number of arguments for 'incr' command")},
		{args: []string{"INCRBY", "n"}, want: resp.Error("ERR wrong number of arguments for 'incrby' command")},

		// INCR keeps the timeout
		{args: []string{"SET", "t", "5", "EX", "10"}, want: resp.SimpleString("OK")},
		{advance: 2 * time.Second, args: []string{"INCR", "t"}, want: resp.Integer(6)},
		{args: []string{"TTL", "t"}, want: resp.Integer(8)},

		// APPEND and STRLEN
		{args: []string{"STRLEN", "s"}, want: resp.Integer(0)},
		{args: []string{"APPEND", "s", "salom"}, want: resp.Integer(5)},
		{args: []string{"APPEND", "s", " dunyo"}, want: resp.Integer(11)},
		{args: []string{"GET", "s"}, want: resp.BulkString("salom dunyo")},
		{args: []string{"STRLEN", "s"}, want: resp.Integer(11)},
		{args: []string{"APPEND", "s"}, want: resp.Error("ERR wrong number of arguments for 'append' command")},

		// MSET and MGET
		{args: []string{"MSET", "a", "1", "b", "2"}, want: resp.SimpleString("OK")},
		{
			args: []string{"MGET", "a", "yoq", "b"},
			want: resp.Array{resp.BulkString("1"), resp.Null{}, resp.BulkString("2")},
		},
		{args: []string{"MSET", "a", "1", "b"}, want: resp.Error("ERR wrong number of arguments for 'mset' command")},
		{args: []string{"MSET", "a"}, want: resp.Error("ERR wrong number of arguments for 'mset' command")},
		{args: []string{"MGET"}, want: resp.Error("ERR wrong number of arguments for 'mget' command")},
	}

	// Steps depend on each other, so stop at the first failure: it is the real cause
	for i, st := range steps {
		advance(st.advance)
		if got := Execute(s, st.args); !reflect.DeepEqual(got, st.want) {
			t.Fatalf("step %d %q: got %#v, want %#v", i+1, st.args, got, st.want)
		}
	}
}

func TestSetNXXX(t *testing.T) {
	s, _ := newTestStore()

	steps := []struct {
		args []string
		want resp.Reply
	}{
		{args: []string{"SET", "k", "1", "XX"}, want: resp.Null{}},
		{args: []string{"GET", "k"}, want: resp.Null{}},
		{args: []string{"SET", "k", "1", "NX"}, want: resp.SimpleString("OK")},
		{args: []string{"SET", "k", "2", "nx"}, want: resp.Null{}},
		{args: []string{"GET", "k"}, want: resp.BulkString("1")},
		{args: []string{"SET", "k", "3", "xx"}, want: resp.SimpleString("OK")},
		{args: []string{"GET", "k"}, want: resp.BulkString("3")},

		// Combined with a timeout, in any order
		{args: []string{"SET", "lock", "a", "NX", "EX", "10"}, want: resp.SimpleString("OK")},
		{args: []string{"TTL", "lock"}, want: resp.Integer(10)},
		{args: []string{"SET", "lock", "b", "PX", "500", "NX"}, want: resp.Null{}},
		{args: []string{"GET", "lock"}, want: resp.BulkString("a")},

		// Errors
		{args: []string{"SET", "k", "v", "NX", "XX"}, want: errSyntax},
		{args: []string{"SET", "k", "v", "NX", "NX"}, want: errSyntax},
		{args: []string{"SET", "k", "v", "NX", "EX"}, want: errSyntax},
		{args: []string{"SET", "k", "v", "EX", "10", "PX", "100"}, want: errSyntax},
		// A rejected SET does not touch the old value
		{args: []string{"GET", "k"}, want: resp.BulkString("3")},
	}

	for i, st := range steps {
		if got := Execute(s, st.args); !reflect.DeepEqual(got, st.want) {
			t.Fatalf("step %d %q: got %#v, want %#v", i+1, st.args, got, st.want)
		}
	}
}
