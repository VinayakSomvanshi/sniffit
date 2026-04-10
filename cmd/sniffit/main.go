package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/vinayak/sniffit/internal/alerting"
	"github.com/vinayak/sniffit/internal/capture"
	"github.com/vinayak/sniffit/internal/rules"
)

func main() {
	iface := flag.String("iface", "eth0", "Interface to sniff on")
	amqpPort := flag.Int("port", 5672, "Primary AMQP port to sniff")
	mgmtPort := flag.Int("mgmt-port", 15672, "RabbitMQ Management API port to sniff")
	controlPlane := flag.String("control-plane", "", "Control plane URL (overrides CONTROL_PLANE_URL env)")
	displayName := flag.String("name", "", "Custom display name for this sniffer")
	targetHost := flag.String("target-host", "", "Optional: Only sniff traffic to/from this specific IP/hostname")
	flag.Parse()

	ports := []int{*amqpPort, *mgmtPort}

	if *controlPlane != "" {
		os.Setenv("CONTROL_PLANE_URL", *controlPlane)
	}
	if *displayName != "" {
		os.Setenv("DISPLAY_NAME", *displayName)
	}

	log.Printf("Starting SniffIt...")

	engine := rules.NewEngine()
	dispatcher := alerting.NewDispatcher()
	sniffer := capture.NewSniffer(*iface, ports, *targetHost, engine, dispatcher)

	go func() {
		if err := sniffer.Start(); err != nil {
			log.Fatalf("Failed to start sniffer: %v", err)
		}
	}()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	log.Println("Shutting down SniffIt gracefully...")
}
