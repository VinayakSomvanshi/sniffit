package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/vinayak/sniffit/internal/controlplane"
	"github.com/vinayak/sniffit/internal/store"
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

	// Initialize database (SQLite by default, PostgreSQL if DATABASE_URL is set)
	db, err := store.NewStore(context.Background())
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	server := controlplane.NewServer(*addr, db)

	// Graceful shutdown
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		log.Println("Shutting down...")
		os.Exit(0)
	}()

	log.Printf("Starting SniffIt Control Plane and Web UI on %s (TLS: %v)...", *addr, *tlsCert != "")
	if err := server.Start(*tlsCert, *tlsKey); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
