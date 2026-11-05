package command

import (
	"reflect"
	"testing"

	"godis/internal/resp"
	"godis/internal/store"
)

func TestExecute(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want resp.Reply
	}{
		{name: "ping", args: []string{"PING"}, want: resp.SimpleString("PONG")},
		{name: "ping lowercase", args: []string{"ping"}, want: resp.SimpleString("PONG")},
		{name: "ping with message", args: []string{"PING", "salom"}, want: resp.BulkString("salom")},
		{
			name: "ping too many args",
			args: []string{"PING", "a", "b"},
			want: resp.Error("ERR wrong number of arguments for 'ping' command"),
		},
		{name: "echo", args: []string{"ECHO", "salom"}, want: resp.BulkString("salom")},
		{name: "echo mixed case", args: []string{"eChO", "salom dunyo"}, want: resp.BulkString("salom dunyo")},
		{
			name: "echo without args",
			args: []string{"ECHO"},
			want: resp.Error("ERR wrong number of arguments for 'echo' command"),
		},
		{
			name: "echo too many args",
			args: []string{"ECHO", "a", "b"},
			want: resp.Error("ERR wrong number of arguments for 'echo' command"),
		},
		{name: "unknown command", args: []string{"foo"}, want: resp.Error("ERR unknown command 'foo'")},
		{name: "empty command", args: []string{}, want: resp.Error("ERR empty command")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// DeepEqual also compares types, so BulkString("x") != SimpleString("x")
			if got := Execute(store.New(), tt.args); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}

// Commands run in order against one store, the way a client session would
func TestExecuteKeyCommands(t *testing.T) {
	s := store.New()

	steps := []struct {
		args []string
		want resp.Reply
	}{
		{args: []string{"GET", "ism"}, want: resp.Null{}},
		{args: []string{"SET", "ism", "Ali"}, want: resp.SimpleString("OK")},
		{args: []string{"GET", "ism"}, want: resp.BulkString("Ali")},
		{args: []string{"set", "ism", "Vali"}, want: resp.SimpleString("OK")},
		{args: []string{"GET", "ism"}, want: resp.BulkString("Vali")},
		{args: []string{"SET", "bosh", ""}, want: resp.SimpleString("OK")},
		{args: []string{"GET", "bosh"}, want: resp.BulkString("")},
		{args: []string{"EXISTS", "ism", "ism", "yoq"}, want: resp.Integer(2)},
		{args: []string{"DEL", "ism", "bosh", "yoq"}, want: resp.Integer(2)},
		{args: []string{"EXISTS", "ism"}, want: resp.Integer(0)},
		{args: []string{"GET", "ism"}, want: resp.Null{}},

		// Arity and syntax errors
		{args: []string{"SET", "ism"}, want: resp.Error("ERR wrong number of arguments for 'set' command")},
		{args: []string{"SET", "ism", "Ali", "EX"}, want: resp.Error("ERR syntax error")},
		{args: []string{"SET", "k", "v", "foo"}, want: resp.Error("ERR syntax error")},
		// A failed SET must not change the store
		{args: []string{"EXISTS", "k"}, want: resp.Integer(0)},
		{args: []string{"GET"}, want: resp.Error("ERR wrong number of arguments for 'get' command")},
		{args: []string{"GET", "a", "b"}, want: resp.Error("ERR wrong number of arguments for 'get' command")},
		{args: []string{"DEL"}, want: resp.Error("ERR wrong number of arguments for 'del' command")},
		{args: []string{"EXISTS"}, want: resp.Error("ERR wrong number of arguments for 'exists' command")},
	}

	// Steps depend on each other, so stop at the first failure: it is the real cause
	for i, st := range steps {
		if got := Execute(s, st.args); !reflect.DeepEqual(got, st.want) {
			t.Fatalf("step %d %q: got %#v, want %#v", i+1, st.args, got, st.want)
		}
	}
}
