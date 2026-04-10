package rules

import (
	"encoding/binary"
	"net/http"
	"testing"

	"github.com/vinayak/sniffit/internal/amqp"
)

func TestEngine_EvaluateMethod_Connection(t *testing.T) {
	engine := NewEngine()

	tests := []struct {
		name       string
		method     *amqp.MethodPayload
		wantRuleID string
		wantSev    string
	}{
		{
			name: "Auth Refused",
			method: &amqp.MethodPayload{
				ClassID:  10,
				MethodID: 50,
				Args:     buildConnectionCloseArgs(403, "ACCESS_REFUSED"),
			},
			wantRuleID: "1.1",
			wantSev:    "error",
		},
		{
			name: "Missed Heartbeats",
			method: &amqp.MethodPayload{
				ClassID:  10,
				MethodID: 50,
				Args:     buildConnectionCloseArgs(320, "missed heartbeats"),
			},
			wantRuleID: "1.2",
			wantSev:    "error",
		},
		{
			name: "Connection Opened",
			method: &amqp.MethodPayload{
				ClassID:  10,
				MethodID: 40, // connection.open
			},
			wantRuleID: "0.3",
			wantSev:    "info",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			alert := engine.EvaluateMethod(tt.method)
			if alert == nil {
				t.Fatalf("expected alert")
			}
			if alert.RuleID != tt.wantRuleID {
				t.Errorf("got RuleID %q, want %q", alert.RuleID, tt.wantRuleID)
			}
			if alert.Severity != tt.wantSev {
				t.Errorf("got Severity %q, want %q", alert.Severity, tt.wantSev)
			}
		})
	}
}

func TestEngine_EvaluateMethod_Queue(t *testing.T) {
	engine := NewEngine()

	// queue.declare (10, 40)
	buf := make([]byte, 100)
	buf[2] = byte(len("test-queue"))
	copy(buf[3:], "test-queue")

	method := &amqp.MethodPayload{
		ClassID:  40,
		MethodID: 10,
		Args:     buf,
	}

	alert := engine.EvaluateMethod(method)
	if alert == nil || alert.RuleID != "0.10" {
		t.Errorf("expected queue_declare alert, got %v", alert)
	}
	if alert.Entity != "test-queue" {
		t.Errorf("expected entity 'test-queue', got %q", alert.Entity)
	}
}

func TestEngine_EvaluateHTTPResponse(t *testing.T) {
	engine := NewEngine()

	// 401 Unauthorized
	resp := &http.Response{StatusCode: 401}
	alert := engine.EvaluateHTTPResponse(resp)
	if alert == nil || alert.RuleID != "5.1" {
		t.Errorf("expected management_auth_failure, got %v", alert)
	}

	// 404 Not Found
	resp = &http.Response{StatusCode: 404}
	alert = engine.EvaluateHTTPResponse(resp)
	if alert == nil || alert.RuleID != "5.2" {
		t.Errorf("expected management_api_error, got %v", alert)
	}

	// 200 OK
	resp = &http.Response{StatusCode: 200}
	alert = engine.EvaluateHTTPResponse(resp)
	if alert != nil {
		t.Errorf("expected nil for 200 OK, got %v", alert)
	}
}

func buildConnectionCloseArgs(replyCode uint16, text string) []byte {
	buf := make([]byte, 2+1+len(text)+4)
	binary.BigEndian.PutUint16(buf[0:2], replyCode)
	buf[2] = byte(len(text))
	copy(buf[3:], text)
	return buf
}
