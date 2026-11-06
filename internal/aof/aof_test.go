package aof

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"godis/internal/resp"
)

// load replays path and returns every command it saw.
func load(t *testing.T, path string) ([][]string, error) {
	t.Helper()
	var got [][]string
	_, err := Load(path, func(args []string) error {
		got = append(got, args)
		return nil
	})
	return got, err
}

func appendAll(t *testing.T, path string, policy Policy, cmds [][]string) {
	t.Helper()
	l, err := Open(path, policy)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for _, c := range cmds {
		if err := l.Append(c); err != nil {
			t.Fatalf("Append(%q): %v", c, err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestAppendLoad(t *testing.T) {
	cmds := [][]string{
		{"SET", "ism", "Ali"},
		{"SET", "bosh", ""},
		// Values with CRLF and spaces survive, because bulk strings are length-prefixed
		{"SET", "matn", "salom\r\ndunyo"},
		{"DEL", "ism"},
	}

	for _, policy := range []Policy{Always, EverySec, No} {
		path := filepath.Join(t.TempDir(), "test.aof")
		appendAll(t, path, policy, cmds)

		got, err := load(t, path)
		if err != nil {
			t.Fatalf("policy %d: Load: %v", policy, err)
		}
		if !reflect.DeepEqual(got, cmds) {
			t.Errorf("policy %d: loaded %q, want %q", policy, got, cmds)
		}
	}
}

// Reopening appends after the old commands instead of overwriting them
func TestReopenAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.aof")
	appendAll(t, path, Always, [][]string{{"SET", "a", "1"}})
	appendAll(t, path, Always, [][]string{{"SET", "b", "2"}})

	got, err := load(t, path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := [][]string{{"SET", "a", "1"}, {"SET", "b", "2"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("loaded %q, want %q", got, want)
	}
}

func TestLoadMissingFile(t *testing.T) {
	n, err := Load(filepath.Join(t.TempDir(), "yoq.aof"), func([]string) error { return nil })
	if n != 0 || err != nil {
		t.Errorf("Load of missing file = %d, %v; want 0, nil", n, err)
	}
}

// The server died in the middle of an append: the half-written tail is cut
// off, the complete commands are kept, and the next append starts cleanly
func TestLoadTruncatedTail(t *testing.T) {
	good := "*3\r\n$3\r\nSET\r\n$1\r\na\r\n$1\r\n1\r\n"
	tails := map[string]string{
		"inside a bulk string": "*3\r\n$3\r\nSET\r\n$1\r\nb\r\n$1\r\n",
		"between elements":     "*3\r\n$3\r\nSET\r\n",
		"inside the header":    "*3\r",
		"only a star":          "*",
	}

	for name, tail := range tails {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "test.aof")
			if err := os.WriteFile(path, []byte(good+tail), 0o644); err != nil {
				t.Fatal(err)
			}

			got, err := load(t, path)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if want := [][]string{{"SET", "a", "1"}}; !reflect.DeepEqual(got, want) {
				t.Fatalf("loaded %q, want %q", got, want)
			}

			data, _ := os.ReadFile(path)
			if string(data) != good {
				t.Fatalf("file after Load = %q, want only the complete command", data)
			}

			// A new append after the cut must load cleanly
			appendAll(t, path, Always, [][]string{{"SET", "c", "3"}})
			got, err = load(t, path)
			if err != nil {
				t.Fatalf("Load after append: %v", err)
			}
			if want := [][]string{{"SET", "a", "1"}, {"SET", "c", "3"}}; !reflect.DeepEqual(got, want) {
				t.Errorf("loaded %q, want %q", got, want)
			}
		})
	}
}

// Garbage in the middle of the file is corruption, not a torn write:
// Load refuses to start instead of silently dropping data after it
func TestLoadCorruptMiddle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.aof")
	data := "*1\r\n$4\r\nPING\r\n" + "GARBAGE\r\n" + "*1\r\n$4\r\nPING\r\n"
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := load(t, path); !errors.Is(err, resp.ErrProtocol) {
		t.Fatalf("Load error = %v, want ErrProtocol", err)
	}
	// The file is left as it was, so a human can inspect it
	if got, _ := os.ReadFile(path); string(got) != data {
		t.Error("Load changed a corrupt file")
	}
}

func TestLoadApplyError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.aof")
	appendAll(t, path, Always, [][]string{{"SET", "a", "1"}, {"BAD"}})

	boom := errors.New("boom")
	n, err := Load(path, func(args []string) error {
		if args[0] == "BAD" {
			return boom
		}
		return nil
	})
	if n != 1 || !errors.Is(err, boom) {
		t.Errorf("Load = %d, %v; want 1, boom", n, err)
	}
}

func TestParsePolicy(t *testing.T) {
	for in, want := range map[string]Policy{"always": Always, "everysec": EverySec, "no": No} {
		if got, err := ParsePolicy(in); err != nil || got != want {
			t.Errorf("ParsePolicy(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := ParsePolicy("sometimes"); err == nil {
		t.Error("ParsePolicy(\"sometimes\") returned no error")
	}
}
