package server

import (
	"errors"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"godis/internal/aof"
	"godis/internal/command"
	"godis/internal/resp"
	"godis/internal/store"
)

type Server struct {
	addr  string
	store *store.Store // shared by all connections

	// aof is nil when persistence is off. writeMu makes "run a write command"
	// and "append it to the AOF" one step: without it two clients could apply
	// SET k 1 and SET k 2 in one order and log them in the other, and after a
	// restart k would hold the wrong value
	aof     *aof.Log
	writeMu sync.Mutex
	bg      sync.WaitGroup // running background AOF rewrites

	// mu guards the fields below: Start and Stop run in different goroutines
	mu       sync.Mutex
	listener net.Listener
	conns    map[net.Conn]struct{} // open client connections
	closed   bool                  // Stop was called

	wg sync.WaitGroup // running handleConnection goroutines
}

// New returns a server with an empty store and no persistence.
func New(addr string) *Server {
	return NewWithStore(addr, store.New(), nil)
}

// NewWithStore returns a server that serves st, for example one already
// filled from an AOF, and appends write commands to log unless it is nil.
// The caller still owns log and closes it after Start returns.
func NewWithStore(addr string, st *store.Store, log *aof.Log) *Server {
	return &Server{
		addr:  addr,
		store: st,
		aof:   log,
		conns: make(map[net.Conn]struct{}),
	}
}

// Start listens and serves clients until Stop is called.
// It returns only after every connection handler has finished.
func (s *Server) Start() error {
	listener, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	defer listener.Close()

	s.mu.Lock()
	if s.closed {
		// Stop came before the listener existed; nothing to serve
		s.mu.Unlock()
		return nil
	}
	s.listener = listener
	s.mu.Unlock()

	log.Printf("godis listening on %s", listener.Addr())

	// Background cleanup of expired keys lives as long as the server runs
	stopExpiry := s.store.StartExpiry(100 * time.Millisecond)
	defer stopExpiry()

	for {
		conn, err := listener.Accept()
		if err != nil {
			// Listener was closed by Stop(): normal shutdown, not an error
			if errors.Is(err, net.ErrClosed) {
				// Stop closed the connections too; wait until their handlers exit,
				// then for a running rewrite, so the caller can close the AOF safely
				s.wg.Wait()
				s.bg.Wait()
				return nil
			}
			// log the error and continue accepting new connections
			log.Printf("accept error: %v", err)
			continue
		}

		if !s.track(conn) {
			// Stop ran between Accept and here
			conn.Close()
			continue
		}
		s.wg.Add(1)
		go s.handleConnection(conn)
	}
}

// Stop begins a graceful shutdown and makes Start return once every
// handler has finished. It stops accepting clients and wakes handlers that
// are waiting for a command; a handler busy with a command finishes it and
// writes the reply first. Stop does not wait: Start's caller waits for Start
// to return, and calls Close if that takes too long.
func (s *Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true

	// A deadline in the past fails only the next Read, unlike Close it
	// leaves a reply that is being written alone
	now := time.Now()
	for conn := range s.conns {
		conn.SetReadDeadline(now)
	}
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

// Close stops the server like Stop and also closes every client connection
// right away. It is the fallback when a handler is stuck, e.g. in a Write
// to a client that does not read its replies.
func (s *Server) Close() error {
	err := s.Stop()

	s.mu.Lock()
	defer s.mu.Unlock()
	for conn := range s.conns {
		conn.Close()
	}
	return err
}

// Addr returns the address the server listens on, or nil before Start
// has opened the listener. With port 0 it shows the port the OS picked.
func (s *Server) Addr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.listener == nil {
		return nil
	}
	return s.listener.Addr()
}

// track registers conn as open; it reports false if the server is stopping.
func (s *Server) track(conn net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return false
	}
	s.conns[conn] = struct{}{}
	return true
}

func (s *Server) untrack(conn net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.conns, conn)
}

func (s *Server) handleConnection(conn net.Conn) {
	defer s.wg.Done()
	defer s.untrack(conn)
	defer conn.Close()

	addr := conn.RemoteAddr()
	log.Printf("New connection from %s", addr)

	r := resp.NewReader(conn)
	w := resp.NewWriter(conn)

	for {
		args, err := r.ReadCommand()
		if err != nil {
			switch {
			case errors.Is(err, io.EOF):
				// Client disconnected between commands: normal close
				log.Printf("Connection from %s closed", addr)
			case errors.Is(err, os.ErrDeadlineExceeded), errors.Is(err, net.ErrClosed):
				// Stop set a read deadline, or Close closed the connection
				log.Printf("Connection from %s closed by shutdown", addr)
			case errors.Is(err, resp.ErrProtocol):
				// Tell the client what went wrong; the stream is out of sync, so close it
				log.Printf("Protocol error from %s: %v", addr, err)
				w.WriteError("ERR Protocol error")
				w.Flush()
			default:
				log.Printf("Read error from %s: %v", addr, err)
			}
			return
		}

		if err := w.WriteReply(s.execute(args)); err != nil {
			log.Printf("Error writing reply to %s: %v", addr, err)
			return
		}
		if err := w.Flush(); err != nil {
			log.Printf("Error flushing reply to %s: %v", addr, err)
			return
		}
	}
}

// execute runs one command and, with persistence on, logs it to the AOF.
func (s *Server) execute(args []string) resp.Reply {
	// BGREWRITEAOF needs the server, not just the store, so it is handled here
	if len(args) > 0 && strings.EqualFold(args[0], "BGREWRITEAOF") {
		return s.bgRewriteAOF(args)
	}

	// Reads never touch the AOF, so they skip writeMu and run in parallel
	if s.aof == nil || len(args) == 0 || !command.IsWrite(args[0]) {
		return command.Execute(s.store, args)
	}

	s.writeMu.Lock()
	reply, logged := command.Run(s.store, args)
	for _, cmd := range logged {
		if err := s.aof.Append(cmd); err != nil {
			// The change is already in memory; the client still gets its
			// reply, but a restart would lose this write
			log.Printf("aof: append %q: %v", cmd, err)
		}
	}
	s.writeMu.Unlock()

	// After the unlock: starting a rewrite takes writeMu itself
	if len(logged) > 0 && s.aof.NeedsRewrite() {
		if err := s.startRewrite(); err != nil && !errors.Is(err, aof.ErrRewriteInProgress) {
			log.Printf("aof: automatic rewrite: %v", err)
		}
	}
	return reply
}

func (s *Server) bgRewriteAOF(args []string) resp.Reply {
	if len(args) != 1 {
		return resp.Error("ERR wrong number of arguments for 'bgrewriteaof' command")
	}
	if s.aof == nil {
		return resp.Error("ERR AOF is turned off, start the server with -appendonly")
	}

	err := s.startRewrite()
	if errors.Is(err, aof.ErrRewriteInProgress) {
		return resp.Error("ERR Background append only file rewriting already in progress")
	}
	if err != nil {
		return resp.Error("ERR " + err.Error())
	}
	return resp.SimpleString("Background append only file rewriting started")
}

// startRewrite takes a snapshot of the store and writes the new AOF in the
// background. Writes are paused only for the snapshot itself, which is done
// in memory; the slow part, writing it to disk, runs while clients keep writing.
func (s *Server) startRewrite() error {
	// Under writeMu no write is half done (applied but not yet logged), so the
	// snapshot and the buffer that starts here split the writes exactly:
	// everything before is in the snapshot, everything after is in the buffer
	s.writeMu.Lock()
	rw, err := s.aof.StartRewrite()
	if err != nil {
		s.writeMu.Unlock()
		return err
	}
	snapshot := s.store.Dump()
	s.writeMu.Unlock()

	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		defer rw.Abort() // no-op after a successful Commit

		before := s.aof.Size()
		for _, cmd := range snapshot {
			if err := rw.Write(cmd); err != nil {
				log.Printf("aof: rewrite failed, keeping the old file: %v", err)
				return
			}
		}
		if err := rw.Commit(); err != nil {
			log.Printf("aof: rewrite failed, keeping the old file: %v", err)
			return
		}
		log.Printf("aof: rewrite done, %d commands, %d -> %d bytes", len(snapshot), before, s.aof.Size())
	}()
	return nil
}
