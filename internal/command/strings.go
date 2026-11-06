package command

import (
	"math"
	"strconv"

	"godis/internal/resp"
	"godis/internal/store"
)

var errOverflow = resp.Error("ERR increment or decrement would overflow")

func incr(c *Context, args []string) resp.Reply {
	s := c.Store

	return incrBy(s, args[1], 1)
}

func decr(c *Context, args []string) resp.Reply {
	s := c.Store

	return incrBy(s, args[1], -1)
}

func incrby(c *Context, args []string) resp.Reply {
	s := c.Store

	delta, err := strconv.ParseInt(args[2], 10, 64)
	if err != nil {
		return errNotInteger
	}
	return incrBy(s, args[1], delta)
}

func decrby(c *Context, args []string) resp.Reply {
	s := c.Store

	delta, err := strconv.ParseInt(args[2], 10, 64)
	if err != nil {
		return errNotInteger
	}
	// -MinInt64 does not fit in int64: it wraps around to MinInt64 itself
	if delta == math.MinInt64 {
		return resp.Error("ERR decrement would overflow")
	}
	return incrBy(s, args[1], -delta)
}

// incrBy runs the shared part of INCR, DECR, INCRBY and DECRBY.
func incrBy(s *store.Store, key string, delta int64) resp.Reply {
	n, err := s.IncrBy(key, delta)
	if err != nil {
		return storeError(err)
	}
	return resp.Integer(n)
}

// appendCmd handles APPEND; "append" is a Go builtin, hence the name.
func appendCmd(c *Context, args []string) resp.Reply {
	s := c.Store

	return intReply(s.Append(args[1], args[2]))
}

func strlen(c *Context, args []string) resp.Reply {
	s := c.Store

	return intReply(s.Strlen(args[1]))
}

// mget returns an array with one element per key: the value, or Null if missing.
func mget(c *Context, args []string) resp.Reply {
	s := c.Store

	values := s.MGet(args[1:]...)
	reply := make(resp.Array, len(values))
	for i, v := range values {
		if v == nil {
			reply[i] = resp.Null{}
		} else {
			reply[i] = resp.BulkString(*v)
		}
	}
	return reply
}

// mset handles MSET key value [key value ...].
func mset(c *Context, args []string) resp.Reply {
	s := c.Store

	// Arity only guarantees at least one pair; the count must also be even
	if len(args[1:])%2 != 0 {
		return wrongArgs(args[0])
	}
	s.MSet(args[1:]...)
	return resp.SimpleString("OK")
}

// intReply turns a store result that is a count or a length into a reply.
func intReply(n int, err error) resp.Reply {
	if err != nil {
		return storeError(err)
	}
	return resp.Integer(n)
}
