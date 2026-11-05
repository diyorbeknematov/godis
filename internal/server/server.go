package server

import (
	"errors"
	"io"
	"log"
	"net"

	"godis/internal/command"
	"godis/internal/resp"
	"godis/internal/store"
)

type Server struct {
	addr     string
	listener net.Listener
	store    *store.Store // shared by all connections
}

func New(addr string) *Server {
	return &Server{
		addr:  addr,
		store: store.New(),
	}
}

func (s *Server) Start() error {
	listener, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	defer listener.Close()
	s.listener = listener

	log.Printf("godis listening on %s", listener.Addr())

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			// Listener was closed by Stop(): normal shutdown, not an error
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			// log the error and continue accepting new connections
			log.Printf("accept error: %v", err)
			continue
		}

		go s.handleConnection(conn)
	}
}

func (s *Server) Stop() error {
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

func (s *Server) handleConnection(conn net.Conn) {
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

		if err := w.WriteReply(command.Execute(s.store, args)); err != nil {
			log.Printf("Error writing reply to %s: %v", addr, err)
			return
		}
		if err := w.Flush(); err != nil {
			log.Printf("Error flushing reply to %s: %v", addr, err)
			return
		}
	}
}
