package command

import (
	"reflect"
	"sort"
	"testing"

	"godis/internal/resp"
	"godis/internal/store"
)

func arr(items ...string) resp.Array {
	a := make(resp.Array, len(items))
	for i, it := range items {
		a[i] = resp.BulkString(it)
	}
	return a
}

// Commands run in order against one store, the way a client session would
func TestTypeCommands(t *testing.T) {
	s, _ := newTestStore()
	wrongArgsFor := func(name string) resp.Reply {
		return resp.Error("ERR wrong number of arguments for '" + name + "' command")
	}

	steps := []struct {
		args []string
		want resp.Reply
	}{
		// Lists
		{args: []string{"LPUSH", "l", "a", "b"}, want: resp.Integer(2)},
		{args: []string{"RPUSH", "l", "x"}, want: resp.Integer(3)},
		{args: []string{"LRANGE", "l", "0", "-1"}, want: arr("b", "a", "x")},
		{args: []string{"LRANGE", "l", "-1", "-1"}, want: arr("x")},
		{args: []string{"LRANGE", "l", "5", "10"}, want: arr()},
		{args: []string{"LRANGE", "l", "a", "1"}, want: errNotInteger},
		{args: []string{"LLEN", "l"}, want: resp.Integer(3)},
		{args: []string{"LPOP", "l"}, want: resp.BulkString("b")},
		{args: []string{"RPOP", "l"}, want: resp.BulkString("x")},
		{args: []string{"RPOP", "l"}, want: resp.BulkString("a")},
		{args: []string{"RPOP", "l"}, want: resp.Null{}},
		{args: []string{"TYPE", "l"}, want: resp.SimpleString("none")},
		{args: []string{"LPUSH", "l"}, want: wrongArgsFor("lpush")},

		// Hashes
		{args: []string{"HSET", "u", "ism", "Ali", "yosh", "20"}, want: resp.Integer(2)},
		{args: []string{"HSET", "u", "yosh", "21"}, want: resp.Integer(0)},
		{args: []string{"HGET", "u", "yosh"}, want: resp.BulkString("21")},
		{args: []string{"HGET", "u", "yoq"}, want: resp.Null{}},
		{args: []string{"HGET", "yoq", "f"}, want: resp.Null{}},
		{args: []string{"HLEN", "u"}, want: resp.Integer(2)},
		{args: []string{"HSET", "u", "f"}, want: wrongArgsFor("hset")},
		{args: []string{"HSET", "u", "a", "1", "b"}, want: wrongArgsFor("hset")},
		{args: []string{"HDEL", "u", "ism", "yoq"}, want: resp.Integer(1)},
		{args: []string{"HGETALL", "u"}, want: arr("yosh", "21")},
		{args: []string{"HGETALL", "yoq"}, want: arr()},

		// Sets
		{args: []string{"SADD", "s", "a", "b", "a"}, want: resp.Integer(2)},
		{args: []string{"SISMEMBER", "s", "a"}, want: resp.Integer(1)},
		{args: []string{"SISMEMBER", "s", "z"}, want: resp.Integer(0)},
		{args: []string{"SCARD", "s"}, want: resp.Integer(2)},
		{args: []string{"SREM", "s", "a"}, want: resp.Integer(1)},
		{args: []string{"SMEMBERS", "s"}, want: arr("b")},

		// TYPE and WRONGTYPE across commands
		{args: []string{"SET", "str", "v"}, want: resp.SimpleString("OK")},
		{args: []string{"TYPE", "str"}, want: resp.SimpleString("string")},
		{args: []string{"TYPE", "u"}, want: resp.SimpleString("hash")},
		{args: []string{"TYPE", "s"}, want: resp.SimpleString("set")},
		{args: []string{"LPUSH", "str", "x"}, want: errWrongType},
		{args: []string{"GET", "s"}, want: errWrongType},
		{args: []string{"INCR", "u"}, want: errWrongType},
		{args: []string{"APPEND", "s", "x"}, want: errWrongType},
		{args: []string{"STRLEN", "u"}, want: errWrongType},
		{args: []string{"HGET", "str", "f"}, want: errWrongType},
		{args: []string{"SADD", "u", "m"}, want: errWrongType},
		{args: []string{"LRANGE", "s", "0", "-1"}, want: errWrongType},
		{args: []string{"MGET", "str", "s"}, want: resp.Array{resp.BulkString("v"), resp.Null{}}},
		// The failed commands did not change anything
		{args: []string{"GET", "str"}, want: resp.BulkString("v")},
		{args: []string{"SCARD", "s"}, want: resp.Integer(1)},
		// Generic commands work on every type
		{args: []string{"EXISTS", "str", "u", "s"}, want: resp.Integer(3)},
		{args: []string{"DEL", "u", "s"}, want: resp.Integer(2)},
	}

	for i, st := range steps {
		if got := Execute(s, st.args); !reflect.DeepEqual(got, st.want) {
			t.Fatalf("step %d %q: got %#v, want %#v", i+1, st.args, got, st.want)
		}
	}
}

// Write commands on the new types go to the AOF and replay to the same data;
// a pop on an empty list changed nothing and is not logged
func TestTypeCommandsReplay(t *testing.T) {
	orig, _ := newTestStore()
	var logged [][]string
	run := func(args ...string) {
		_, cmds := Run(orig, args)
		logged = append(logged, cmds...)
	}

	run("RPUSH", "l", "a", "b", "c")
	run("LPOP", "l")
	run("LPOP", "yoq")
	run("HSET", "h", "f1", "v1", "f2", "v2")
	run("HDEL", "h", "f1")
	run("SADD", "s", "x", "y")
	run("SREM", "s", "x")

	for _, cmd := range logged {
		if cmd[0] == "LPOP" && cmd[1] == "yoq" {
			t.Errorf("no-op LPOP was logged")
		}
	}

	replayed := store.NewWithClock(orig.Now)
	for _, args := range logged {
		Run(replayed, args)
	}

	for _, q := range [][]string{
		{"LRANGE", "l", "0", "-1"},
		{"HGETALL", "h"},
		{"SMEMBERS", "s"},
	} {
		want := sorted(Execute(orig, q))
		got := sorted(Execute(replayed, q))
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q: replayed %#v, original %#v", q, got, want)
		}
	}
}

// sorted orders an array reply, for types whose order is random.
func sorted(r resp.Reply) []string {
	a, _ := r.(resp.Array)
	out := make([]string, len(a))
	for i, it := range a {
		out[i] = string(it.(resp.BulkString))
	}
	sort.Strings(out)
	return out
}
