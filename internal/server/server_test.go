package server

import (
	"bufio"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"godis/internal/aof"
	"godis/internal/command"
	"godis/internal/store"
)

// startTestServer runs a server on a free port and returns its address and Start's result.
func startTestServer(t *testing.T) (*Server, string, <-chan error) {
	t.Helper()
	s := New("127.0.0.1:0") // port 0: the OS picks a free one
	errc := make(chan error, 1)
	go func() { errc <- s.Start() }()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if a := s.Addr(); a != nil {
			return s, a.String(), errc
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("server did not start listening within 1s")
	return nil, "", nil
}

// waitStart fails the test if Start does not return nil within a second.
func waitStart(t *testing.T, errc <-chan error) {
	t.Helper()
	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("Start returned %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Start did not return within 1s")
	}
}

// ping sends PING and checks the reply.
func ping(t *testing.T, conn net.Conn, r *bufio.Reader) {
	t.Helper()
	if _, err := conn.Write([]byte("*1\r\n$4\r\nPING\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if line, err := r.ReadString('\n'); err != nil || line != "+PONG\r\n" {
		t.Fatalf("reply = %q, %v; want \"+PONG\\r\\n\"", line, err)
	}
}

// assertClosed checks that the server closed its side of conn.
func assertClosed(t *testing.T, conn net.Conn, r *bufio.Reader) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := r.ReadByte(); err == nil {
		t.Error("connection still open")
	}
}

// Stop wakes an idle handler with a read deadline, the handler closes the connection
func TestStopWithOpenConnection(t *testing.T) {
	s, addr, errc := startTestServer(t)

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	r := bufio.NewReader(conn)

	// One round trip proves the connection is being served
	ping(t, conn, r)

	if err := s.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	waitStart(t, errc)
	assertClosed(t, conn, r)
}

// Close is the forced fallback: it closes connections directly
func TestClose(t *testing.T) {
	s, addr, errc := startTestServer(t)

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	r := bufio.NewReader(conn)
	ping(t, conn, r)

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	waitStart(t, errc)
	assertClosed(t, conn, r)
}

func TestStopTwice(t *testing.T) {
	s, _, errc := startTestServer(t)
	if err := s.Stop(); err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	if err := s.Stop(); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
	waitStart(t, errc)
}

// Stop before Start must not leave Start serving forever
func TestStopBeforeStart(t *testing.T) {
	s := New("127.0.0.1:0")
	s.Stop()

	errc := make(chan error, 1)
	go func() { errc <- s.Start() }()
	select {
	case <-errc:
	case <-time.After(time.Second):
		t.Fatal("Start kept running after an earlier Stop")
	}
}

// replay loads an AOF into a fresh store, as main does on startup.
func replay(t *testing.T, path string) *store.Store {
	t.Helper()
	st := store.New()
	if _, err := aof.Load(path, func(args []string) error {
		command.Execute(st, args)
		return nil
	}); err != nil {
		t.Fatalf("Load: %v", err)
	}
	return st
}

// Many goroutines write the same key at once. The AOF must log the writes
// in the order the store applied them, or the replayed value differs
func TestAOFOrderMatchesStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.aof")
	log, err := aof.Open(path, aof.No)
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithStore("127.0.0.1:0", store.New(), log)

	var wg sync.WaitGroup
	for g := 0; g < 20; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				s.execute([]string{"SET", "k", strconv.Itoa(g*1000 + i)})
				s.execute([]string{"INCR", "n"})
			}
		}(g)
	}
	wg.Wait()
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}

	replayed := replay(t, path)
	for _, key := range []string{"k", "n"} {
		want, _, _ := s.store.Get(key)
		got, _, _ := replayed.Get(key)
		if got != want {
			t.Errorf("%s: replayed %q, in memory %q", key, got, want)
		}
	}
	if got, _, _ := replayed.Get("n"); got != "1000" {
		t.Errorf("n after replay = %q, want \"1000\"", got)
	}
}

// Full cycle over TCP: write, shut down, start again from the same file
func TestAOFRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.aof")

	runServer := func(st *store.Store, send string) string {
		log, err := aof.Open(path, aof.Always)
		if err != nil {
			t.Fatal(err)
		}
		s := NewWithStore("127.0.0.1:0", st, log)
		errc := make(chan error, 1)
		go func() { errc <- s.Start() }()

		var addr net.Addr
		for deadline := time.Now().Add(time.Second); addr == nil && time.Now().Before(deadline); {
			addr = s.Addr()
			time.Sleep(time.Millisecond)
		}
		if addr == nil {
			t.Fatal("server did not start")
		}

		conn, err := net.Dial("tcp", addr.String())
		if err != nil {
			t.Fatal(err)
		}
		conn.Write([]byte(send))
		// One reply line per command is enough for these tests
		r := bufio.NewReader(conn)
		var replies strings.Builder
		for i := 0; i < strings.Count(send, "*"); i++ {
			line, err := r.ReadString('\n')
			if err != nil {
				t.Fatalf("read reply: %v", err)
			}
			replies.WriteString(line)
			if strings.HasPrefix(line, "$") && line != "$-1\r\n" {
				body, _ := r.ReadString('\n')
				replies.WriteString(body)
			}
		}
		conn.Close()

		s.Stop()
		waitStart(t, errc)
		if err := log.Close(); err != nil {
			t.Fatal(err)
		}
		return replies.String()
	}

	cmd := func(args ...string) string {
		var b strings.Builder
		b.WriteString("*" + strconv.Itoa(len(args)) + "\r\n")
		for _, a := range args {
			b.WriteString("$" + strconv.Itoa(len(a)) + "\r\n" + a + "\r\n")
		}
		return b.String()
	}

	runServer(store.New(), cmd("SET", "ism", "Ali")+cmd("INCR", "n")+cmd("INCR", "n")+cmd("SET", "t", "x", "EX", "100"))

	// "Restart": a new store filled only from the file
	got := runServer(replay(t, path), cmd("GET", "ism")+cmd("GET", "n")+cmd("TTL", "t"))
	want := "$3\r\nAli\r\n" + "$1\r\n2\r\n" + ":100\r\n"
	if got != want {
		t.Errorf("after restart: replies %q, want %q", got, want)
	}
}
