package alerting

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/vinayak/sniffit/internal/correlator"
	"github.com/vinayak/sniffit/internal/rules"
)

type Dispatcher struct {
	client     *http.Client
	controlURL string
}

func NewDispatcher() *Dispatcher {
	url := os.Getenv("CONTROL_PLANE_URL")
	if url == "" {
		url = "http://localhost:8080" // Default for testing — matches control-plane default port
	}
	return &Dispatcher{
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
		controlURL: url + "/api/alerts",
	}
}

func (d *Dispatcher) Dispatch(alert *rules.Alert) {
	alert.PodCPUPct = correlator.GetCPUPercent()
	alert.PodMemMB = correlator.GetMemoryMB()

	// In real K8s, these would come from downward API ENVs
	alert.Pod = os.Getenv("POD_NAME")
	if alert.Pod == "" {
		alert.Pod = "sniffit-sidecar"
	}
	alert.Namespace = os.Getenv("POD_NAMESPACE")
	if alert.Namespace == "" {
		alert.Namespace = "external" // Default for host mode
	}

	// Override with custom display name if provided
	if displayName := os.Getenv("DISPLAY_NAME"); displayName != "" {
		alert.Pod = displayName
	}

	payload, err := json.MarshalIndent(alert, "", "  ")
	if err != nil {
		log.Printf("Error marshaling alert: %v", err)
		return
	}

	resp, err := d.client.Post(d.controlURL, "application/json", bytes.NewBuffer(payload))
	if err != nil {
		log.Printf("Failed to dispatch alert to %s: %v", d.controlURL, err)
		// Fallback to logging
		log.Printf("== ALARM TRIGGERED ==\n%s\n=====================", string(payload))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		log.Printf("[dispatcher] Control plane returned non-2xx status: %d for rule %s", resp.StatusCode, alert.RuleName)
	} else {
		log.Printf("[dispatcher] Alert dispatched successfully: rule=%s severity=%s pod=%s", alert.RuleName, alert.Severity, alert.Pod)
	}
}
