package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"godis/internal/aof"
	"godis/internal/command"
	"godis/internal/resp"
	"godis/internal/server"
	"godis/internal/store"
)

// shutdownTimeout is how long running commands get to finish after Ctrl+C.
const shutdownTimeout = 5 * time.Second

func main() {
	// Same flag names as redis-server. The default host only accepts
	// connections from this machine: godis has no password, so listening
	// on the network must be a deliberate choice
	bind := flag.String("bind", "127.0.0.1", "interface to listen on; use 0.0.0.0 to accept connections from other machines")
	port := flag.Int("port", 6379, "TCP port to listen on (0 lets the OS pick a free one)")
	appendOnly := flag.Bool("appendonly", false, "persist write commands to an append-only file and replay it on startup")
	aofPath := flag.String("appendfilename", "appendonly.aof", "path of the append-only file")
	fsync := flag.String("appendfsync", "everysec", "how often to fsync the append-only file: always, everysec or no")
	flag.Parse()

	if *port < 0 || *port > 65535 {
		log.Fatalf("invalid -port %d: must be between 0 and 65535", *port)
	}
	// JoinHostPort adds brackets for IPv6: "::1" and 6379 give "[::1]:6379",
	// while plain "::1" + ":" + "6379" would be ambiguous
	addr := net.JoinHostPort(*bind, strconv.Itoa(*port))

	// ctx is cancelled on Ctrl+C (SIGINT) or SIGTERM (e.g. from docker stop)
	ctx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()

	st := store.New()
	var aofLog *aof.Log
	if *appendOnly {
		policy, err := aof.ParsePolicy(*fsync)
		if err != nil {
			log.Fatal(err)
		}

		// Replay before the server starts, so no client sees half-loaded data
		start := time.Now()
		n, err := aof.Load(*aofPath, func(args []string) error {
			if r, isErr := command.Execute(st, args).(resp.Error); isErr {
				return errors.New(string(r))
			}
			return nil
		})
		if err != nil {
			log.Fatalf("Loading %s: %v", *aofPath, err)
		}
		log.Printf("Loaded %d commands from %s in %s", n, *aofPath, time.Since(start).Round(time.Millisecond))

		if aofLog, err = aof.Open(*aofPath, policy); err != nil {
			log.Fatalf("Opening %s: %v", *aofPath, err)
		}
		// Runs after Start has returned, so every handler is done writing
		defer func() {
			if err := aofLog.Close(); err != nil {
				log.Printf("Closing %s: %v", *aofPath, err)
			}
		}()
	}

	srv := server.NewWithStore(addr, st, aofLog)

	errc := make(chan error, 1)
	go func() {
		errc <- srv.Start()
	}()

	select {
	case err := <-errc:
		// Start failed on its own, e.g. the port is busy
		if err != nil {
			log.Fatalf("Server failed to start: %v", err)
		}
		return
	case <-ctx.Done():
	}

	log.Println("Shutting down...")
	// A second Ctrl+C now kills the process right away, in case shutdown hangs
	stopSignals()

	if err := srv.Stop(); err != nil {
		log.Printf("Stop error: %v", err)
	}

	// Start returns after all connections and the expiry goroutine have finished.
	// Give running commands shutdownTimeout to finish, then force the rest closed
	var err error
	select {
	case err = <-errc:
	case <-time.After(shutdownTimeout):
		log.Printf("Shutdown took longer than %s, closing remaining connections", shutdownTimeout)
		srv.Close()
		err = <-errc
	}
	if err != nil {
		log.Printf("Server error: %v", err)
	}
	log.Println("Server stopped")
}
