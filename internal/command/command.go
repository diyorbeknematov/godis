package command

import (
	"strings"

	"godis/internal/resp"
	"godis/internal/store"
)

// Command describes one Redis command.
type Command struct {
	// Arity is the number of arguments including the command name.
	// A negative value means "at least -Arity", e.g. -2 is "2 or more".
	Arity   int
	Handler func(s *store.Store, args []string) resp.Reply
}

// commands maps an upper-case command name to its definition.
var commands = map[string]Command{
	"PING": {Arity: -1, Handler: ping},
	"ECHO": {Arity: 2, Handler: echo},

	"SET":    {Arity: -3, Handler: set},
	"GET":    {Arity: 2, Handler: get},
	"DEL":    {Arity: -2, Handler: del},
	"EXISTS": {Arity: -2, Handler: exists},
}

// Execute looks up the command, checks its arity and runs it against s.
// Handlers can trust that the arity check has already passed.
func Execute(s *store.Store, args []string) resp.Reply {
	// ReadCommand can return an empty array for "*0\r\n"
	if len(args) == 0 {
		return resp.Error("ERR empty command")
	}

	cmd, ok := commands[strings.ToUpper(args[0])]
	if !ok {
		// Redis echoes the name exactly as the client sent it
		return resp.Error("ERR unknown command '" + args[0] + "'")
	}

	if !arityOK(cmd.Arity, len(args)) {
		return wrongArgs(args[0])
	}

	return cmd.Handler(s, args)
}

func arityOK(arity, n int) bool {
	if arity >= 0 {
		return n == arity
	}
	return n >= -arity
}

func wrongArgs(name string) resp.Reply {
	return resp.Error("ERR wrong number of arguments for '" + strings.ToLower(name) + "' command")
}

// ping replies PONG, or echoes its single optional argument.
func ping(_ *store.Store, args []string) resp.Reply {
	switch len(args) {
	case 1:
		return resp.SimpleString("PONG")
	case 2:
		return resp.BulkString(args[1])
	default:
		// Arity -1 only guarantees the minimum, the maximum is checked here
		return wrongArgs(args[0])
	}
}

func echo(_ *store.Store, args []string) resp.Reply {
	return resp.BulkString(args[1])
}

// set stores a value. Options like EX/NX are not supported yet.
func set(s *store.Store, args []string) resp.Reply {
	if len(args) > 3 {
		return resp.Error("ERR syntax error")
	}
	s.Set(args[1], args[2])
	return resp.SimpleString("OK")
}

// get returns the value, or Null if the key does not exist.
func get(s *store.Store, args []string) resp.Reply {
	value, ok := s.Get(args[1])
	if !ok {
		return resp.Null{}
	}
	return resp.BulkString(value)
}

func del(s *store.Store, args []string) resp.Reply {
	return resp.Integer(s.Del(args[1:]...))
}

func exists(s *store.Store, args []string) resp.Reply {
	return resp.Integer(s.Exists(args[1:]...))
}
