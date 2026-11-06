package command

import (
	"math"
	"strconv"
	"time"

	"godis/internal/resp"
	"godis/internal/store"
)

var (
	errSyntax     = resp.Error("ERR syntax error")
	errNotInteger = resp.Error("ERR value is not an integer or out of range")
)

// invalidExpire is the reply for an expire time the command cannot use.
func invalidExpire(cmd string) resp.Reply {
	return resp.Error("ERR invalid expire time in '" + cmd + "' command")
}

// parseTTL converts a client number in the given unit to a Duration.
// cmd is the lower-case command name used in the error message.
// Duration counts nanoseconds in an int64 (about 292 years), so a large
// value multiplied by unit would overflow and wrap around to a negative
// number; that is checked before multiplying.
func parseTTL(arg string, unit time.Duration, cmd string) (time.Duration, resp.Reply) {
	n, err := strconv.ParseInt(arg, 10, 64)
	if err != nil {
		return 0, errNotInteger
	}
	if n > math.MaxInt64/int64(unit) || n < math.MinInt64/int64(unit) {
		return 0, invalidExpire(cmd)
	}
	return time.Duration(n) * unit, nil
}

// expire handles EXPIRE key seconds. A non-positive value deletes the key.
// It is logged as PEXPIREAT with an absolute deadline: replaying "EXPIRE k 10"
// an hour later would give the key 10 more seconds it never had.
func expire(c *Context, args []string) resp.Reply {
	s := c.Store

	d, errReply := parseTTL(args[2], time.Second, "expire")
	if errReply != nil {
		return errReply
	}
	at := s.Now().Add(d).Truncate(time.Millisecond)
	if !s.ExpireAt(args[1], at) {
		c.PropagateNothing()
		return resp.Integer(0)
	}
	c.Propagate("PEXPIREAT", args[1], strconv.FormatInt(at.UnixMilli(), 10))
	return resp.Integer(1)
}

// pexpireat handles PEXPIREAT key unix-ms: a deadline as an absolute time.
func pexpireat(c *Context, args []string) resp.Reply {
	ms, err := strconv.ParseInt(args[2], 10, 64)
	if err != nil {
		return errNotInteger
	}
	return boolReply(c.Store.ExpireAt(args[1], time.UnixMilli(ms)))
}

// ttl handles TTL key: remaining seconds, -2 if missing, -1 if no expiry.
func ttl(c *Context, args []string) resp.Reply {
	s := c.Store

	return remaining(s.TTL(args[1]), time.Second)
}

// pttl handles PTTL key: same as TTL but in milliseconds.
func pttl(c *Context, args []string) resp.Reply {
	s := c.Store

	return remaining(s.TTL(args[1]), time.Millisecond)
}

// persist handles PERSIST key: 1 if a timeout was removed, 0 otherwise.
func persist(c *Context, args []string) resp.Reply {
	s := c.Store

	return boolReply(s.Persist(args[1]))
}

// remaining converts a store TTL to the client's unit.
// The special values are mapped directly; real durations are rounded
// to the nearest unit, so 9.6s is reported as 10 like in Redis.
func remaining(d, unit time.Duration) resp.Reply {
	switch d {
	case store.KeyMissing:
		return resp.Integer(-2)
	case store.NoExpiry:
		return resp.Integer(-1)
	}
	return resp.Integer(d.Round(unit) / unit)
}

func boolReply(b bool) resp.Reply {
	if b {
		return resp.Integer(1)
	}
	return resp.Integer(0)
}
