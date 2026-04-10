package ai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/vinayak/sniffit/internal/rules"
)

func TestAgent_Diagnose(t *testing.T) {
	// 1. Mock Server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}{
			Choices: []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			}{
				{
					Message: struct {
						Content string `json:"content"`
					}{Content: "Mocked AI Diagnosis Result"},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// 2. Setup Agent
	os.Setenv("OLLAMA_MODEL", "ignored")
	os.Setenv("OLLAMA_URL", server.URL)
	agent := NewAgent()

	// 3. Test
	alert := &rules.Alert{PlainEnglish: "Connection refused", RuleName: "auth_fail"}
	diagnosis, err := agent.Diagnose(alert)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if diagnosis != "Mocked AI Diagnosis Result" {
		t.Errorf("Unexpected diagnosis: %q", diagnosis)
	}
}

func TestAgent_GenerateHealthSummary_HeuristicFallback(t *testing.T) {
	// No API key -> should use heuristic
	os.Unsetenv("OPENROUTER_API_KEY")
	os.Unsetenv("OLLAMA_MODEL")
	agent := NewAgent()

	alerts := []*rules.Alert{
		{Severity: "error"},
		{Severity: "error"},
		{Severity: "error"},
		{Severity: "warn"},
	}

	summary, err := agent.GenerateHealthSummary(alerts)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if !summary.Heuristic {
		t.Error("Expected heuristic fallback")
	}
	if summary.Status != "critical" {
		t.Errorf("Expected critical status for 2 errors, got %q", summary.Status)
	}
}

func TestExtractSection(t *testing.T) {
	text := "ISSUES: problem here. PATTERNS: pattern here. ACTIONS: fix here."
	issues := extractSection(text, "ISSUES:", []string{"PATTERNS:", "ACTIONS:"})
	if issues != "problem here." {
		t.Errorf("Extracted %q", issues)
	}
}
