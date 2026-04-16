package main

import (
	"flag"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/vinayak/sniffit/internal/alerting"
	"github.com/vinayak/sniffit/internal/capture"
	"github.com/vinayak/sniffit/internal/logging"
	"github.com/vinayak/sniffit/internal/rules"
)

func main() {
	logging.RegisterFlags()

	ifaceStr := flag.String("iface", "eth0", "Comma-separated list of interfaces to sniff on (e.g. eth0,lo)")
	amqpPort := flag.Int("port", 5672, "Primary AMQP port to sniff")
	mgmtPort := flag.Int("mgmt-port", 15672, "RabbitMQ Management API port to sniff")
	controlPlane := flag.String("control-plane", "", "Control plane URL (overrides CONTROL_PLANE_URL env)")
	displayName := flag.String("name", "", "Custom display name for this sniffer")
	targetHost := flag.String("target-host", "", "Optional: Only sniff traffic to/from this specific IP/hostname")
	flag.Parse()

	// Apply log level from flag or env
	logging.InitFromEnvOrFlag()
	logging.Info("log level set to %s", logging.GlobalLogLevel.String())

	interfaces := strings.Split(*ifaceStr, ",")
	ports := []int{*amqpPort, *mgmtPort}

	if *controlPlane != "" {
		os.Setenv("CONTROL_PLANE_URL", *controlPlane)
	}
	if *displayName != "" {
		os.Setenv("DISPLAY_NAME", *displayName)
	}

	logging.Info("Starting SniffIt on interfaces: %v", interfaces)

	engine := rules.NewEngine()
	dispatcher := alerting.NewDispatcher()

	var wg sync.WaitGroup
	for _, iface := range interfaces {
		iface = strings.TrimSpace(iface)
		if iface == "" {
			continue
		}

		wg.Add(1)
		go func(itf string) {
			defer wg.Done()
			sniffer := capture.NewSniffer(itf, ports, *targetHost, engine, dispatcher)
			logging.Info("Starting sniffer loop for interface: %s", itf)
			if err := sniffer.Start(); err != nil {
				logging.Error("Failed to start sniffer on %s: %v", itf, err)
			}
		}(iface)
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	logging.Info("Shutting down SniffIt gracefully...")
}
