package controlplane

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/vinayak/sniffit/internal/rules"
)

func TestServer_HandleAlerts(t *testing.T) {
	// 1. Setup
	os.Setenv("WEBHOOK_URL", "http://test")
	server := NewServer()
	
	// Ensure fresh state
	server.alerts = []*rules.Alert{}

	// 2. Test POST /api/alerts
	alert := rules.Alert{RuleID: "test.1", Severity: "error", RuleName: "Test Alert"}
	body, _ := json.Marshal(alert)
	req := httptest.NewRequest("POST", "/api/alerts", bytes.NewReader(body))
	rr := httptest.NewRecorder()

	server.handleAlerts(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Errorf("Expected 202 Accepted, got %d", rr.Code)
	}

	// 3. Test GET /api/alerts/history
	req = httptest.NewRequest("GET", "/api/alerts/history", nil)
	rr = httptest.NewRecorder()
	server.handleGetAlerts(rr, req)

	var history []*rules.Alert
	json.Unmarshal(rr.Body.Bytes(), &history)

	if len(history) != 1 || history[0].RuleID != "test.1" {
		t.Errorf("Unexpected history length or content: %v", history)
	}

	// 4. Test DELETE /api/alerts
	req = httptest.NewRequest("DELETE", "/api/alerts", nil)
	rr = httptest.NewRecorder()
	server.handleAlerts(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected 200 OK, got %d", rr.Code)
	}
	if len(server.alerts) != 0 {
		t.Error("Alerts should be cleared")
	}
}

func TestServer_HandleSettings(t *testing.T) {
	server := NewServer()

	// Update settings
	updated := Settings{AITriggerLevel: "info", MinNotifySev: "warn"}
	body, _ := json.Marshal(updated)
	req := httptest.NewRequest("POST", "/api/settings", bytes.NewReader(body))
	rr := httptest.NewRecorder()

	server.handleSettings(rr, req)

	if server.settings.AITriggerLevel != "info" {
		t.Errorf("Expected AITriggerLevel 'info', got %q", server.settings.AITriggerLevel)
	}
}
