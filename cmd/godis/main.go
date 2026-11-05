package main

import (
	"godis/internal/server"
	"log"
)

func main() {
	serve := server.New("localhost:6378")
	if err := serve.Start(); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}


}
