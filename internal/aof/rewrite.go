package aof

import (
	"errors"
	"os"
	"path/filepath"

	"godis/internal/resp"
)

// ErrRewriteInProgress is returned by StartRewrite while another rewrite runs.
var ErrRewriteInProgress = errors.New("aof: rewrite already in progress")

// Rewrite builds a compact replacement for the AOF in a temporary file.
//
// The steps, and why they are in this order:
//
//  1. StartRewrite: from now on every Append is also buffered.
//  2. The caller takes a snapshot of the data and writes it with Write.
//     Steps 1 and 2 must happen with writes paused (the server holds its
//     write mutex): a write that lands in the store before the snapshot but
//     is appended after StartRewrite would be in the snapshot and in the
//     buffer, and applied twice on replay, e.g. INCR counted twice.
//  3. Commit: the buffered commands go after the snapshot, then the
//     temporary file atomically replaces the old one.
//
// Only steps 1 and 2 need writes paused. Writing the snapshot to disk, the
// slow part, happens while clients keep writing.
type Rewrite struct {
	l    *Log
	f    *os.File
	cw   *countingWriter
	w    *resp.Writer
	done bool
}

// StartRewrite begins a rewrite. Until Commit or Abort, appended commands
// are buffered for the new file as well as written to the old one.
func (l *Log) StartRewrite() (*Rewrite, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.rewriting {
		return nil, ErrRewriteInProgress
	}

	// Same directory as the AOF, so the final rename stays on one filesystem
	// and is atomic
	f, err := os.CreateTemp(filepath.Dir(l.path), filepath.Base(l.path)+".rewrite-*")
	if err != nil {
		return nil, err
	}

	l.rewriting = true
	l.rewriteBuf = nil
	cw := &countingWriter{w: f}
	return &Rewrite{l: l, f: f, cw: cw, w: resp.NewWriter(cw)}, nil
}

// Write adds one command of the snapshot to the new file.
// It does not take the log's lock, so it never blocks Append.
func (r *Rewrite) Write(args []string) error {
	return r.w.WriteReply(bulkArray(args))
}

// Commit finishes the rewrite: it appends the commands buffered since
// StartRewrite, syncs the new file to disk and renames it over the old one.
// On error the old file stays in place, complete, and the rewrite is aborted.
func (r *Rewrite) Commit() error {
	l := r.l
	l.mu.Lock()
	defer l.mu.Unlock()

	// Appends wait for l.mu, so no new command can slip in between the
	// buffer and the switch to the new file
	for _, args := range l.rewriteBuf {
		if err := r.w.WriteReply(bulkArray(args)); err != nil {
			r.abortLocked()
			return err
		}
	}
	if err := r.w.Flush(); err != nil {
		r.abortLocked()
		return err
	}
	// The data must be on disk before the rename: otherwise a power loss
	// right after it could leave an empty file in place of the old one
	if err := r.f.Sync(); err != nil {
		r.abortLocked()
		return err
	}
	if err := os.Rename(r.f.Name(), l.path); err != nil {
		r.abortLocked()
		return err
	}
	// fsync the directory too, so the rename itself survives a power loss
	syncDir(filepath.Dir(l.path))

	// The renamed file is the AOF now; keep appending to it
	old := l.f
	l.w.Flush()
	l.setFile(r.f, r.cw.n)
	l.baseSize = r.cw.n
	old.Close()

	l.rewriting = false
	l.rewriteBuf = nil
	r.done = true
	return nil
}

// Abort drops the rewrite and removes the temporary file.
// Calling it after Commit does nothing, so it is safe to defer.
func (r *Rewrite) Abort() {
	r.l.mu.Lock()
	defer r.l.mu.Unlock()
	r.abortLocked()
}

func (r *Rewrite) abortLocked() {
	if r.done {
		return
	}
	r.f.Close()
	os.Remove(r.f.Name())
	r.l.rewriting = false
	r.l.rewriteBuf = nil
	r.done = true
}

func bulkArray(args []string) resp.Array {
	cmd := make(resp.Array, len(args))
	for i, a := range args {
		cmd[i] = resp.BulkString(a)
	}
	return cmd
}

// syncDir fsyncs a directory, making renames inside it durable.
// Errors are ignored: some systems do not support it, and the rename has
// already happened.
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
}
