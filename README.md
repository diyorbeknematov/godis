# godis

A Redis-compatible in-memory key-value server written in Go, built from scratch
with only the standard library. It speaks the RESP2 protocol, so the regular
`redis-cli` and Redis client libraries can talk to it.

```
$ redis-cli -p 6378
127.0.0.1:6378> SET name Ali EX 60
OK
127.0.0.1:6378> GET name
"Ali"
127.0.0.1:6378> TTL name
(integer) 58
```

## Features

- **RESP2 protocol:** parser and writer for arrays, bulk strings, simple
  strings, errors, integers and null, with limits against oversized input.
- **Atomic commands:** `INCR`, `SET NX`, `MSET` and friends read and write
  under one lock, so concurrent clients never lose an update.
- **Concurrent clients:** one goroutine per connection, sharing a single store
  protected by a `sync.RWMutex`.
- **Key expiration** in two ways, as in Redis:
  - *lazy:* an expired key is removed when it is accessed;
  - *active:* a background job samples keys with a timeout every 100 ms and
    deletes the expired ones.
- **Persistence (AOF):** every write command is appended to a file and
  replayed on startup, with `always` / `everysec` / `no` fsync policies.
  Relative timeouts are stored as absolute deadlines, and a half-written
  command at the end of the file (from a crash) is cut off on load.
- **Graceful shutdown:** on `Ctrl+C` / `SIGTERM` the server stops accepting
  clients, lets running commands finish, and force-closes whatever is still
  open after 5 seconds.
- **No dependencies:** standard library only.

## Supported commands

| Command | Syntax | Reply |
|---|---|---|
| `PING` | `PING [message]` | `PONG`, or the message |
| `ECHO` | `ECHO message` | the message |
| `SET` | `SET key value [NX \| XX] [EX seconds \| PX milliseconds \| PXAT unix-ms]` | `OK`, or nil if the `NX` / `XX` condition fails |
| `GET` | `GET key` | the value, or nil |
| `MSET` | `MSET key value [key value ...]` | `OK` |
| `MGET` | `MGET key [key ...]` | array of values, nil for missing keys |
| `INCR` / `DECR` | `INCR key` | the new value |
| `INCRBY` / `DECRBY` | `INCRBY key delta` | the new value |
| `APPEND` | `APPEND key value` | length of the new string |
| `STRLEN` | `STRLEN key` | length in bytes, `0` if the key does not exist |
| `DEL` | `DEL key [key ...]` | number of keys removed |
| `EXISTS` | `EXISTS key [key ...]` | number of keys that exist |
| `EXPIRE` | `EXPIRE key seconds` | `1` if the timeout was set, `0` if the key does not exist |
| `PEXPIREAT` | `PEXPIREAT key unix-ms` | same as `EXPIRE`, with an absolute deadline |
| `TTL` | `TTL key` | seconds left, `-1` with no timeout, `-2` if the key does not exist |
| `PTTL` | `PTTL key` | same as `TTL`, in milliseconds |
| `PERSIST` | `PERSIST key` | `1` if a timeout was removed, `0` otherwise |

Command names are case-insensitive. Error messages follow Redis, for example
`ERR wrong number of arguments for 'get' command`.

## Getting started

Requires Go 1.27 or newer.

```sh
go build -o godis ./cmd/godis
./godis -port 6378
```

Then connect from another terminal:

```sh
redis-cli -p 6378 ping
```

### Options

| Flag | Default | Description |
|---|---|---|
| `-bind` | `127.0.0.1` | Interface to listen on. Use `0.0.0.0` to accept connections from other machines. |
| `-port` | `6379` | TCP port. `0` lets the OS pick a free one. |
| `-appendonly` | `false` | Persist write commands to an append-only file and replay it on startup. |
| `-appendfilename` | `appendonly.aof` | Path of the append-only file. |
| `-appendfsync` | `everysec` | When to fsync the file: `always`, `everysec` or `no`. |

To keep data across restarts:

```sh
./godis -port 6378 -appendonly
```

> **Security:** godis has no authentication. Keep the default `-bind 127.0.0.1`
> unless the machine is on a trusted network.

Run `./godis -h` for the full list.

## Testing

```sh
go test -race ./...
go vet ./...
```

Expiration is tested with a fake clock injected into the store, so the tests
do not sleep and give the same result on every run. Server tests listen on
`127.0.0.1:0`, so they get a free port and never clash with a running server.

## Project layout

```
cmd/godis/        entry point: flags, signal handling, shutdown timeout
internal/resp/    RESP2 reader, writer and reply types
internal/command/ command table, argument checks and handlers
internal/store/   concurrent key-value store with expiration
internal/aof/     append-only file: append, fsync policies, load and repair
internal/server/  TCP server, per-connection loop, graceful shutdown
```

A request flows through the layers like this:

```
TCP connection → resp.Reader → command.Execute → store.Store
                                     ↓
                 resp.Writer ← resp.Reply
```

Each layer can be tested on its own: `command.Execute` takes a slice of
strings and returns a `resp.Reply`, without any network or I/O.

## Design notes

- **Command table with arity.** Every command declares how many arguments it
  takes, and the check runs in one place before the handler is called, so
  handlers can trust their arguments.
- **Closed reply types.** `resp.Reply` is an interface with an unexported
  method, so only the reply types in the `resp` package can be returned.
- **Reply sanitizing.** `\r` and `\n` in simple strings and errors are replaced
  with spaces, so a client cannot inject a fake reply through a command name.
- **Read lock fast path.** `GET` uses a read lock. Only when it finds an expired
  key does it take the write lock, and then it looks the key up again, because
  another client may have set it in between.
- **Atomic read-modify-write.** `INCR` is one store method, not `GET` + `SET`
  in the handler. Two separate locks would let two clients both read 5 and
  both write 6. That is not a data race, so `-race` cannot catch it; a test
  with 100 concurrent `INCR`s checks that the result is exactly 100.
- **Write log in apply order.** Running a write command and appending it to
  the AOF happen under one mutex. Otherwise two clients could apply
  `SET k 1` and `SET k 2` in one order and log them in the other, and after a
  restart `k` would hold the wrong value. Reads skip this mutex.
- **Absolute deadlines in the AOF.** `SET k v EX 10` is logged as
  `SET k v PXAT <unix-ms>` and `EXPIRE` as `PEXPIREAT`, so replaying the file
  an hour later does not give keys time they never had.
- **Overflow-safe timeouts.** `SET k v EX 99999999999` is rejected instead of
  overflowing `time.Duration` and expiring right away.
- **Bounded active expiry.** Each cleanup round holds the write lock, so the
  number of rounds and the number of keys looked at per round are capped.

## Limitations

- Only string values. No lists, hashes, sets or sorted sets.
- The AOF only grows: there is no rewrite yet, so 1000 `SET`s to one key
  are 1000 entries in the file.
- No authentication, no TLS.
- Active expiry walks past keys without a timeout. With many such keys, keys
  that do have a timeout are sampled less often. Redis avoids this by keeping
  keys with a timeout in a separate table.

## Roadmap

- [ ] AOF rewrite, to compact the file
- [ ] More data types: lists, hashes
- [ ] `KEYS`, `FLUSHALL`
