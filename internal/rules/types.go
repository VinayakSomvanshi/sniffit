package rules

import (
	"time"
)

// Alert represents any observed AMQP event — from critical errors to
// informational lifecycle events. Severity can be "error", "warn", or "info".
type Alert struct {
	ID           int64     `json:"id,omitempty"` // database primary key
	TenantID     string    `json:"tenant_id,omitempty"`
	Severity     string    `json:"severity"`
	RuleID       string    `json:"rule_id"`
	RuleName     string    `json:"rule_name"`
	MethodName   string    `json:"method_name,omitempty"` // AMQP class.method string e.g. "channel.close"
	Pod          string    `json:"pod,omitempty"`
	Namespace    string    `json:"namespace,omitempty"`
	Entity       string    `json:"entity,omitempty"`
	PlainEnglish string    `json:"plain_english"`
	PodCPUPct    float64   `json:"pod_cpu_pct"`
	PodMemMB     float64   `json:"pod_mem_mb"`
	Timestamp    time.Time `json:"timestamp"`
	AmqpFrameRaw string    `json:"amqp_frame_raw"`
	AIDiagnosis  string    `json:"ai_diagnosis,omitempty"`
	CreatedAt    time.Time `json:"created_at,omitempty"`
}
