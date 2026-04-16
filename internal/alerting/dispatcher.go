package alerting

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/vinayak/sniffit/internal/correlator"
	"github.com/vinayak/sniffit/internal/logging"
	"github.com/vinayak/sniffit/internal/rules"
)

type Dispatcher struct {
	client     *http.Client
	controlURL string
}

func NewDispatcher() *Dispatcher {
	url := os.Getenv("CONTROL_PLANE_URL")
	if url == "" {
		url = "http://localhost:8080"
	}

	insecure := os.Getenv("INSECURE_SKIP_VERIFY") == "true"
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure},
	}

	return &Dispatcher{
		client: &http.Client{
			Transport: tr,
			Timeout:   5 * time.Second,
		},
		controlURL: url + "/api/alerts",
	}
}

func (d *Dispatcher) Dispatch(alert *rules.Alert) {
	alert.PodCPUPct = correlator.GetCPUPercent()
	alert.PodMemMB = correlator.GetMemoryMB()

	alert.TenantID = os.Getenv("TENANT_ID")
	if alert.TenantID == "" {
		alert.TenantID = "default"
	}

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
		logging.Debug("Error marshaling alert: %v", err)
		return
	}

	req, err := http.NewRequest("POST", d.controlURL, bytes.NewBuffer(payload))
	if err != nil {
		logging.Debug("Failed to map alert to request for %s: %v", d.controlURL, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey := os.Getenv("API_KEY"); apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := d.client.Do(req)
	if err != nil {
		logging.Debug("Failed to dispatch alert to %s: %v", d.controlURL, err)
		// Fallback to logging
		logging.Debug("== ALARM TRIGGERED ==\n%s\n=====================", string(payload))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		logging.Debug("[dispatcher] Control plane returned non-2xx status: %d for rule %s", resp.StatusCode, alert.RuleName)
	} else {
		logging.Debug("[dispatcher] Alert dispatched successfully: rule=%s severity=%s pod=%s", alert.RuleName, alert.Severity, alert.Pod)
	}
}
