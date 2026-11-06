package aof

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRewrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.aof")
	l, err := Open(path, Always)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	// History that the snapshot below compacts
	for i := 0; i < 100; i++ {
		l.Append([]string{"INCR", "n"})
	}
	sizeBefore := l.Size()

	rw, err := l.StartRewrite()
	if err != nil {
		t.Fatal(err)
	}
	// A client writes while the snapshot is being saved
	l.Append([]string{"SET", "during", "1"})
	if err := rw.Write([]string{"SET", "n", "100"}); err != nil {
		t.Fatal(err)
	}
	l.Append([]string{"INCR", "n"})
	if err := rw.Commit(); err != nil {
		t.Fatal(err)
	}
	// And after the switch to the new file
	l.Append([]string{"SET", "after", "1"})

	got, err := load(t, path)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"SET", "n", "100"},    // snapshot
		{"SET", "during", "1"}, // buffered during the rewrite
		{"INCR", "n"},          // buffered during the rewrite
		{"SET", "after", "1"},  // appended to the new file
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("file after rewrite = %q, want %q", got, want)
	}
	if l.Size() >= sizeBefore {
		t.Errorf("size after rewrite = %d, before = %d; want smaller", l.Size(), sizeBefore)
	}

	// No temporary file is left behind
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("directory has %d files after rewrite, want 1", len(entries))
	}
}

// An aborted rewrite leaves the old file complete, including what was
// appended during the attempt
func TestRewriteAbort(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.aof")
	l, err := Open(path, Always)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	l.Append([]string{"SET", "a", "1"})
	rw, _ := l.StartRewrite()
	rw.Write([]string{"SET", "a", "1"})
	l.Append([]string{"SET", "b", "2"})
	rw.Abort()
	rw.Abort() // safe to call twice

	got, _ := load(t, path)
	if want := [][]string{{"SET", "a", "1"}, {"SET", "b", "2"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("file after abort = %q, want %q", got, want)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("temporary file not removed: %d files", len(entries))
	}

	// A new rewrite can start after an abort
	rw, err = l.StartRewrite()
	if err != nil {
		t.Fatalf("StartRewrite after abort: %v", err)
	}
	rw.Abort()
}

func TestRewriteOnlyOneAtATime(t *testing.T) {
	l, err := Open(filepath.Join(t.TempDir(), "test.aof"), No)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	rw, _ := l.StartRewrite()
	defer rw.Abort()
	if _, err := l.StartRewrite(); !errors.Is(err, ErrRewriteInProgress) {
		t.Errorf("second StartRewrite error = %v, want ErrRewriteInProgress", err)
	}
}

func TestNeedsRewrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.aof")
	l, err := Open(path, No)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	l.RewriteMinSize = 100

	cmd := []string{"SET", "k", "0123456789"} // 33 bytes in RESP
	for l.Size() < 100 {
		if l.NeedsRewrite() {
			t.Fatalf("NeedsRewrite at %d bytes, below RewriteMinSize", l.Size())
		}
		l.Append(cmd)
	}
	if !l.NeedsRewrite() {
		t.Fatalf("NeedsRewrite = false at %d bytes", l.Size())
	}

	// After a rewrite to 33 bytes, the next one waits until the file doubles
	rw, _ := l.StartRewrite()
	if l.NeedsRewrite() {
		t.Error("NeedsRewrite = true while a rewrite runs")
	}
	rw.Write(cmd)
	rw.Commit()
	l.RewriteMinSize = 0
	if l.NeedsRewrite() {
		t.Errorf("NeedsRewrite right after rewrite at %d bytes", l.Size())
	}
	l.Append(cmd)
	if !l.NeedsRewrite() {
		t.Errorf("NeedsRewrite = false at %d bytes, twice the rewritten size", l.Size())
	}
}
