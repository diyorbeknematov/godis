package command

import (
	"strconv"

	"godis/internal/resp"
)

// typeCmd handles TYPE key; "type" is a Go keyword, hence the name.
func typeCmd(c *Context, args []string) resp.Reply {
	return resp.SimpleString(c.Store.Type(args[1]))
}

// Lists

func lpush(c *Context, args []string) resp.Reply {
	return intReply(c.Store.LPush(args[1], args[2:]...))
}

func rpush(c *Context, args []string) resp.Reply {
	return intReply(c.Store.RPush(args[1], args[2:]...))
}

func lpop(c *Context, args []string) resp.Reply {
	return bulkOrNull(c.Store.LPop(args[1]))
}

func rpop(c *Context, args []string) resp.Reply {
	return bulkOrNull(c.Store.RPop(args[1]))
}

func llen(c *Context, args []string) resp.Reply {
	return intReply(c.Store.LLen(args[1]))
}

// lrange handles LRANGE key start stop.
func lrange(c *Context, args []string) resp.Reply {
	start, err1 := strconv.Atoi(args[2])
	stop, err2 := strconv.Atoi(args[3])
	if err1 != nil || err2 != nil {
		return errNotInteger
	}
	return bulkArray(c.Store.LRange(args[1], start, stop))
}

// Hashes

// hset handles HSET key field value [field value ...].
func hset(c *Context, args []string) resp.Reply {
	// Arity only guarantees one pair; the count must also be even
	if len(args[2:])%2 != 0 {
		return wrongArgs(args[0])
	}
	return intReply(c.Store.HSet(args[1], args[2:]...))
}

func hget(c *Context, args []string) resp.Reply {
	return bulkOrNull(c.Store.HGet(args[1], args[2]))
}

func hdel(c *Context, args []string) resp.Reply {
	return intReply(c.Store.HDel(args[1], args[2:]...))
}

func hgetall(c *Context, args []string) resp.Reply {
	return bulkArray(c.Store.HGetAll(args[1]))
}

func hlen(c *Context, args []string) resp.Reply {
	return intReply(c.Store.HLen(args[1]))
}

// Sets

func sadd(c *Context, args []string) resp.Reply {
	return intReply(c.Store.SAdd(args[1], args[2:]...))
}

func srem(c *Context, args []string) resp.Reply {
	return intReply(c.Store.SRem(args[1], args[2:]...))
}

func sismember(c *Context, args []string) resp.Reply {
	ok, err := c.Store.SIsMember(args[1], args[2])
	if err != nil {
		return storeError(err)
	}
	return boolReply(ok)
}

func smembers(c *Context, args []string) resp.Reply {
	return bulkArray(c.Store.SMembers(args[1]))
}

func scard(c *Context, args []string) resp.Reply {
	return intReply(c.Store.SCard(args[1]))
}

// Reply helpers for store results

// bulkOrNull turns a "value, found, error" result into a bulk string or Null.
func bulkOrNull(v string, ok bool, err error) resp.Reply {
	if err != nil {
		return storeError(err)
	}
	if !ok {
		return resp.Null{}
	}
	return resp.BulkString(v)
}

// bulkArray turns a list of strings into an array of bulk strings.
func bulkArray(items []string, err error) resp.Reply {
	if err != nil {
		return storeError(err)
	}
	a := make(resp.Array, len(items))
	for i, it := range items {
		a[i] = resp.BulkString(it)
	}
	return a
}
