package rules

import (
	"encoding/binary"
	"testing"

	"github.com/vinayak/sniffit/internal/amqp"
)

func TestEngineRuleEvaluations(t *testing.T) {
	engine := NewEngine()

	tests := []struct {
		name       string
		method     *amqp.MethodPayload
		wantRuleID string
	}{
		{
			name: "1.1 Auth Refused",
			method: &amqp.MethodPayload{
				ClassID:  10, // Connection
				MethodID: 50, // Close
				Args:     buildConnectionCloseArgs(403, "ACCESS_REFUSED - Login was refused using authentication mechanism..."),
			},
			wantRuleID: "1.1",
		},
		{
			name: "1.2 Missed heartbeats",
			method: &amqp.MethodPayload{
				ClassID:  10, // Connection
				MethodID: 50, // Close
				Args:     buildConnectionCloseArgs(320, "CONNECTION_FORCED - missed heartbeats from client"),
			},
			wantRuleID: "1.2",
		},
		{
			name: "4.1 Consumer timeout",
			method: &amqp.MethodPayload{
				ClassID:  10, // Connection
				MethodID: 50, // Close
				Args:     buildConnectionCloseArgs(406, "PRECONDITION_FAILED - delivery acknowledgement on channel timed out"),
			},
			wantRuleID: "4.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			alert := engine.EvaluateMethod(tt.method)
			if alert == nil {
				t.Fatalf("expected alert for %s, got nil", tt.name)
			}
			if alert.RuleID != tt.wantRuleID {
				t.Errorf("got RuleID = %q, want %q", alert.RuleID, tt.wantRuleID)
			}
			if alert.Timestamp.IsZero() {
				t.Errorf("expected timestamp to be set")
			}
		})
	}
}

func buildConnectionCloseArgs(replyCode uint16, text string) []byte {
	buf := make([]byte, 2+1+len(text)+4)
	binary.BigEndian.PutUint16(buf[0:2], replyCode)
	buf[2] = byte(len(text))
	copy(buf[3:], text)
	// mock class/method
	return buf
}
