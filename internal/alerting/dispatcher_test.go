package alerting

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/vinayak/sniffit/internal/rules"
)

func TestDispatcher_Dispatch(t *testing.T) {
	// 1. Setup mock control plane server
	var receivedAlert rules.Alert
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/alerts" {
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&receivedAlert); err != nil {
			t.Errorf("Failed to decode alert: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	// 2. Configure dispatcher to use mock server
	os.Setenv("CONTROL_PLANE_URL", server.URL)
	os.Setenv("POD_NAME", "test-pod")
	os.Setenv("POD_NAMESPACE", "test-ns")

	dispatcher := NewDispatcher()
	alert := &rules.Alert{
		RuleName: "test_rule",
		Severity: "warn",
	}

	dispatcher.Dispatch(alert)

	// 3. Verify
	if receivedAlert.RuleName != "test_rule" {
		t.Errorf("Expected RuleName 'test_rule', got %q", receivedAlert.RuleName)
	}
	if receivedAlert.Pod != "test-pod" {
		t.Errorf("Expected Pod 'test-pod', got %q", receivedAlert.Pod)
	}
	if receivedAlert.Namespace != "test-ns" {
		t.Errorf("Expected Namespace 'test-ns', got %q", receivedAlert.Namespace)
	}
}

func TestDispatcher_Dispatch_Error(t *testing.T) {
	// Point to non-existent server
	os.Setenv("CONTROL_PLANE_URL", "http://localhost:1")
	dispatcher := NewDispatcher()
	dispatcher.client.Timeout = 10 * time.Millisecond

	alert := &rules.Alert{RuleName: "error_test"}
	// Should not panic, just log an error
	dispatcher.Dispatch(alert)
}
