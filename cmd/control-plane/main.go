package main

import (
	"flag"
	"log"

	"github.com/joho/godotenv"
	"github.com/vinayak/sniffit/internal/controlplane"
)

func main() {
	addr := flag.String("addr", ":8080", "Address to listen on")
	tlsCert := flag.String("tls-cert", "", "Path to TLS certificate file (enables HTTPS)")
	tlsKey := flag.String("tls-key", "", "Path to TLS key file (enables HTTPS)")
	flag.Parse()

	// Optionally load .env file from the local root
	if err := godotenv.Load(); err != nil {
		log.Println("Note: .env file not found, relying on pure system environment variables")
	}

	server := controlplane.NewServer()
	
	log.Printf("Starting SniffIt Control Plane and Web UI on %s (TLS: %v)...", *addr, *tlsCert != "")
	if err := server.Start(*addr, *tlsCert, *tlsKey); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
