package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/vinayak/sniffit/internal/controlplane"
	"github.com/vinayak/sniffit/internal/logging"
	"github.com/vinayak/sniffit/internal/store"
)

func main() {
	logging.RegisterFlags()

	addr := flag.String("addr", ":8080", "Address to listen on")
	tlsCert := flag.String("tls-cert", "", "Path to TLS certificate file (enables HTTPS)")
	tlsKey := flag.String("tls-key", "", "Path to TLS key file (enables HTTPS)")
	flag.Parse()

	// Apply log level from flag or env
	logging.InitFromEnvOrFlag()
	logging.Info("log level set to %s", logging.GlobalLogLevel.String())

	// Optionally load .env file from the local root
	if err := godotenv.Load(); err != nil {
		logging.Info("Note: .env file not found, relying on pure system environment variables")
	}

	// Initialize database (SQLite by default, PostgreSQL if DATABASE_URL is set)
	db, err := store.NewStore(context.Background())
	if err != nil {
		logging.Error("Failed to initialize database: %v", err)
		os.Exit(1)
	}
	defer db.Close()

	server := controlplane.NewServer(*addr, db)

	// Graceful shutdown
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		logging.Info("Shutting down...")
		os.Exit(0)
	}()

	logging.Info("Starting SniffIt Control Plane and Web UI on %s (TLS: %v)...", *addr, *tlsCert != "")
	if err := server.Start(*tlsCert, *tlsKey); err != nil {
		logging.Error("Server failed: %v", err)
		os.Exit(1)
	}
}
