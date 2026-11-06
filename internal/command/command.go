package command

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"godis/internal/resp"
	"godis/internal/store"
)

// Command describes one Redis command.
type Command struct {
	// Arity is the number of arguments including the command name.
	// A negative value means "at least -Arity", e.g. -2 is "2 or more".
	Arity   int
	Handler func(c *Context, args []string) resp.Reply
	// Write marks commands that change data; only they go to the AOF.
	Write bool
}

// Context is what a handler runs with: the store, plus a way to say what
// should be written to the AOF instead of the command as the client sent it.
type Context struct {
	Store *store.Store

	propagated [][]string
	rewritten  bool
}

// Propagate records args as the command to log in place of the original.
// Handlers use it when the original would replay differently, e.g. a
// relative "EX 10" that must become an absolute deadline.
func (c *Context) Propagate(args ...string) {
	c.rewritten = true
	c.propagated = append(c.propagated, args)
}

// PropagateNothing marks a write command that changed nothing, so nothing is logged.
func (c *Context) PropagateNothing() {
	c.rewritten = true
}

// commands maps an upper-case command name to its definition.
var commands = map[string]Command{
	"PING": {Arity: -1, Handler: ping},
	"ECHO": {Arity: 2, Handler: echo},

	"SET":    {Arity: -3, Handler: set, Write: true},
	"GET":    {Arity: 2, Handler: get},
	"DEL":    {Arity: -2, Handler: del, Write: true},
	"EXISTS": {Arity: -2, Handler: exists},

	"EXPIRE":    {Arity: 3, Handler: expire, Write: true},
	"PEXPIREAT": {Arity: 3, Handler: pexpireat, Write: true},
	"TTL":       {Arity: 2, Handler: ttl},
	"PTTL":      {Arity: 2, Handler: pttl},
	"PERSIST":   {Arity: 2, Handler: persist, Write: true},

	"INCR":   {Arity: 2, Handler: incr, Write: true},
	"DECR":   {Arity: 2, Handler: decr, Write: true},
	"INCRBY": {Arity: 3, Handler: incrby, Write: true},
	"DECRBY": {Arity: 3, Handler: decrby, Write: true},
	"APPEND": {Arity: 3, Handler: appendCmd, Write: true},
	"STRLEN": {Arity: 2, Handler: strlen},
	"MGET":   {Arity: -2, Handler: mget},
	"MSET":   {Arity: -3, Handler: mset, Write: true},

	"TYPE": {Arity: 2, Handler: typeCmd},

	"LPUSH":  {Arity: -3, Handler: lpush, Write: true},
	"RPUSH":  {Arity: -3, Handler: rpush, Write: true},
	"LPOP":   {Arity: 2, Handler: lpop, Write: true},
	"RPOP":   {Arity: 2, Handler: rpop, Write: true},
	"LLEN":   {Arity: 2, Handler: llen},
	"LRANGE": {Arity: 4, Handler: lrange},

	"HSET":    {Arity: -4, Handler: hset, Write: true},
	"HGET":    {Arity: 3, Handler: hget},
	"HDEL":    {Arity: -3, Handler: hdel, Write: true},
	"HGETALL": {Arity: 2, Handler: hgetall},
	"HLEN":    {Arity: 2, Handler: hlen},

	"SADD":      {Arity: -3, Handler: sadd, Write: true},
	"SREM":      {Arity: -3, Handler: srem, Write: true},
	"SISMEMBER": {Arity: 3, Handler: sismember},
	"SMEMBERS":  {Arity: 2, Handler: smembers},
	"SCARD":     {Arity: 2, Handler: scard},
}

// Execute looks up the command, checks its arity and runs it against s.
// Handlers can trust that the arity check has already passed.
func Execute(s *store.Store, args []string) resp.Reply {
	reply, _ := Run(s, args)
	return reply
}

// Run is like Execute but also returns the commands to append to the AOF:
// none for reads and failed writes, the original args for most writes, or
// what the handler passed to Propagate.
func Run(s *store.Store, args []string) (resp.Reply, [][]string) {
	// ReadCommand can return an empty array for "*0\r\n"
	if len(args) == 0 {
		return resp.Error("ERR empty command"), nil
	}

	cmd, ok := commands[strings.ToUpper(args[0])]
	if !ok {
		// Redis echoes the name exactly as the client sent it
		return resp.Error("ERR unknown command '" + args[0] + "'"), nil
	}

	if !arityOK(cmd.Arity, len(args)) {
		return wrongArgs(args[0]), nil
	}

	c := &Context{Store: s}
	reply := cmd.Handler(c, args)

	if !cmd.Write {
		return reply, nil
	}
	switch reply.(type) {
	case resp.Error, resp.Null:
		// Rejected, or SET NX/XX whose condition failed: nothing changed
		return reply, nil
	}
	if c.rewritten {
		return reply, c.propagated
	}
	return reply, [][]string{args}
}

// IsWrite reports whether the command named name changes data.
func IsWrite(name string) bool {
	return commands[strings.ToUpper(name)].Write
}

func arityOK(arity, n int) bool {
	if arity >= 0 {
		return n == arity
	}
	return n >= -arity
}

var errWrongType = resp.Error("WRONGTYPE Operation against a key holding the wrong kind of value")

// storeError turns an error from the store into the reply Redis gives for it.
func storeError(err error) resp.Reply {
	switch {
	case errors.Is(err, store.ErrWrongType):
		return errWrongType
	case errors.Is(err, store.ErrNotInteger):
		return errNotInteger
	case errors.Is(err, store.ErrOverflow):
		return errOverflow
	}
	return resp.Error("ERR " + err.Error())
}

func wrongArgs(name string) resp.Reply {
	return resp.Error("ERR wrong number of arguments for '" + strings.ToLower(name) + "' command")
}

// ping replies PONG, or echoes its single optional argument.
func ping(_ *Context, args []string) resp.Reply {
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

func echo(_ *Context, args []string) resp.Reply {
	return resp.BulkString(args[1])
}

// set handles SET key value [NX | XX] [EX seconds | PX milliseconds | PXAT unix-ms].
// PXAT is what the AOF stores, so a replayed SET keeps its original deadline.
func set(c *Context, args []string) resp.Reply {
	s := c.Store

	var expiresAt time.Time // zero means no expiration
	cond := store.Always
	condArg := ""

	for i := 3; i < len(args); i++ {
		switch opt := strings.ToUpper(args[i]); opt {
		case "NX", "XX":
			// NX and XX together, or one of them twice
			if cond != store.Always {
				return errSyntax
			}
			if opt == "NX" {
				cond = store.IfAbsent
			} else {
				cond = store.IfExists
			}
			condArg = opt

		case "EX", "PX", "PXAT":
			// Missing number, or two timeout options
			if i+1 >= len(args) || !expiresAt.IsZero() {
				return errSyntax
			}
			unit := time.Second
			if opt != "EX" {
				unit = time.Millisecond
			}

			d, errReply := parseTTL(args[i+1], unit, "set")
			if errReply != nil {
				return errReply
			}
			// Unlike EXPIRE, SET does not accept a non-positive time
			if d <= 0 {
				return invalidExpire("set")
			}
			if opt == "PXAT" {
				expiresAt = time.UnixMilli(int64(d / time.Millisecond))
			} else {
				// Millisecond precision, so the deadline in memory and in the AOF are equal
				expiresAt = s.Now().Add(d).Truncate(time.Millisecond)
			}
			i++ // the number is consumed too

		default:
			return errSyntax
		}
	}

	if !s.SetIf(args[1], args[2], expiresAt, cond) {
		// The NX or XX condition did not hold
		return resp.Null{}
	}

	if !expiresAt.IsZero() {
		logged := []string{"SET", args[1], args[2], "PXAT", strconv.FormatInt(expiresAt.UnixMilli(), 10)}
		if condArg != "" {
			logged = append(logged, condArg)
		}
		c.Propagate(logged...)
	}
	return resp.SimpleString("OK")
}

// get returns the value, or Null if the key does not exist.
func get(c *Context, args []string) resp.Reply {
	s := c.Store

	value, ok, err := s.Get(args[1])
	if err != nil {
		return storeError(err)
	}
	if !ok {
		return resp.Null{}
	}
	return resp.BulkString(value)
}

func del(c *Context, args []string) resp.Reply {
	s := c.Store

	return resp.Integer(s.Del(args[1:]...))
}

func exists(c *Context, args []string) resp.Reply {
	s := c.Store

	return resp.Integer(s.Exists(args[1:]...))
}
