// Package aof keeps an append-only file of write commands, so data survives
// a restart: on startup the file is replayed, while running every write is
// appended to it.
package aof

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"time"

	"godis/internal/resp"
)

// Policy says how often the file is fsynced to disk: a trade-off between
// speed and how many recent writes a power loss can take.
type Policy int

const (
	Always   Policy = iota // fsync after every write: safest, slowest
	EverySec               // fsync once a second: lose at most ~1s of writes
	No                     // leave it to the OS: fastest, may lose more
)

// ParsePolicy reads the -appendfsync flag value.
func ParsePolicy(s string) (Policy, error) {
	switch s {
	case "always":
		return Always, nil
	case "everysec":
		return EverySec, nil
	case "no":
		return No, nil
	}
	return 0, fmt.Errorf("invalid fsync policy %q: want always, everysec or no", s)
}

// Log is an open append-only file. It is safe for concurrent use.
type Log struct {
	path   string
	policy Policy

	// RewriteMinSize and the doubling rule decide when NeedsRewrite says yes,
	// like Redis' auto-aof-rewrite-min-size and auto-aof-rewrite-percentage 100
	RewriteMinSize int64

	mu       sync.Mutex
	f        *os.File
	cw       *countingWriter // counts bytes written to f
	w        *resp.Writer
	baseSize int64 // file size after the last rewrite (or at Open)

	// While a rewrite runs, every appended command is also kept here and
	// added to the new file at the end, so nothing written meanwhile is lost
	rewriting  bool
	rewriteBuf [][]string

	stopSync func() // stops the EverySec goroutine; nil for other policies
}

// DefaultRewriteMinSize is the size below which a file is never rewritten automatically.
const DefaultRewriteMinSize = 64 << 20 // 64 MB, as in Redis

// Open opens path for appending, creating it if needed.
func Open(path string, policy Policy) (*Log, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}

	l := &Log{path: path, policy: policy, RewriteMinSize: DefaultRewriteMinSize}
	l.setFile(f, info.Size())
	l.baseSize = info.Size()
	if policy == EverySec {
		l.stopSync = l.startSyncLoop(time.Second)
	}
	return l, nil
}

// setFile makes f the file that Append writes to. size is its current size.
func (l *Log) setFile(f *os.File, size int64) {
	l.f = f
	l.cw = &countingWriter{w: f, n: size}
	l.w = resp.NewWriter(l.cw)
}

// Append writes one command, in the same RESP form clients send.
// The data reaches the OS before Append returns; whether it is on disk
// depends on the policy.
func (l *Log) Append(args []string) error {
	cmd := bulkArray(args)

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.rewriting {
		l.rewriteBuf = append(l.rewriteBuf, args)
	}

	// The old file keeps getting every command too: if the rewrite fails
	// or the server dies during it, the old file is still complete
	if err := l.w.WriteReply(cmd); err != nil {
		return err
	}
	// Without Flush the command would sit in our buffer, where even a crash
	// of just this process (not the machine) loses it
	if err := l.w.Flush(); err != nil {
		return err
	}
	if l.policy == Always {
		return l.f.Sync()
	}
	return nil
}

// NeedsRewrite reports whether the file has grown enough to be worth
// rewriting: at least RewriteMinSize, and twice as big as after the last
// rewrite. It is false while a rewrite is running.
func (l *Log) NeedsRewrite() bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	return !l.rewriting && l.cw.n >= l.RewriteMinSize && l.cw.n >= 2*l.baseSize
}

// Size returns the current size of the file in bytes.
func (l *Log) Size() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.cw.n
}

// Close flushes, fsyncs and closes the file.
func (l *Log) Close() error {
	if l.stopSync != nil {
		l.stopSync()
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.w.Flush(); err != nil {
		l.f.Close()
		return err
	}
	if err := l.f.Sync(); err != nil {
		l.f.Close()
		return err
	}
	return l.f.Close()
}

func (l *Log) startSyncLoop(interval time.Duration) (stop func()) {
	quit := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				l.mu.Lock()
				err := l.f.Sync()
				l.mu.Unlock()
				if err != nil {
					log.Printf("aof: fsync: %v", err)
				}
			case <-quit:
				return
			}
		}
	}()

	var once sync.Once
	return func() {
		once.Do(func() { close(quit) })
		<-done
	}
}

// Load replays every command in path through apply and returns how many
// commands it replayed. A missing file is not an error: it is a fresh start.
//
// If the server died in the middle of an append, the file ends with half a
// command. Load cuts that tail off and keeps the rest, the same as Redis
// with aof-load-truncated. A broken command in the middle of the file is a
// real corruption, so Load stops with an error instead of guessing.
func Load(path string, apply func(args []string) error) (int, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer f.Close()

	cr := &countingReader{r: f}
	r := resp.NewReader(cr)

	n := 0
	for {
		// Bytes of complete commands so far: what was read minus what still sits in the buffer
		good := cr.n - int64(r.Buffered())

		args, err := r.ReadCommand()
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return n, truncateTail(f, good, cr.n)
			}
			return n, fmt.Errorf("aof: command %d at byte %d: %w", n+1, good, err)
		}

		if err := apply(args); err != nil {
			return n, fmt.Errorf("aof: command %d at byte %d %q: %w", n+1, good, args, err)
		}
		n++
	}
}

// truncateTail cuts the file back to good bytes if anything is left after it.
// A plain io.EOF can still leave bytes behind, e.g. "*2\r" without its "\n",
// and new appends after those bytes would corrupt the next command.
func truncateTail(f *os.File, good, size int64) error {
	if good == size {
		return nil
	}
	log.Printf("aof: removing %d bytes of an incomplete command at the end of the file", size-good)
	return f.Truncate(good)
}

// countingReader counts the bytes read through it.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// countingWriter counts the bytes written through it.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
