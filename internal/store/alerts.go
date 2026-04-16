package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/vinayak/sniffit/internal/rules"
)

// ListAlerts returns alerts for a tenant, newest first, with pagination.
func (s *Store) ListAlerts(ctx context.Context, tenantID string, limit, offset int) ([]*rules.Alert, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, tenant_id, severity, rule_id, rule_name, method_name,
		       pod, namespace, entity, plain_english, pod_cpu_pct, pod_mem_mb,
		       timestamp, amqp_frame_raw, ai_diagnosis, created_at
		FROM alerts
		WHERE tenant_id = ?
		ORDER BY timestamp DESC
		LIMIT ? OFFSET ?
	`, tenantID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAlerts(rows)
}

// GetAlertByID returns a single alert.
func (s *Store) GetAlertByID(ctx context.Context, tenantID string, id int64) (*rules.Alert, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, severity, rule_id, rule_name, method_name,
		       pod, namespace, entity, plain_english, pod_cpu_pct, pod_mem_mb,
		       timestamp, amqp_frame_raw, ai_diagnosis, created_at
		FROM alerts
		WHERE tenant_id = ? AND id = ?
	`, tenantID, id)
	a, err := scanAlert(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return a, err
}

// InsertAlert inserts a new alert and returns its database ID.
func (s *Store) InsertAlert(ctx context.Context, a *rules.Alert) (int64, error) {
	ts := a.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	created := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO alerts (tenant_id, severity, rule_id, rule_name, method_name,
			pod, namespace, entity, plain_english, pod_cpu_pct, pod_mem_mb,
			timestamp, amqp_frame_raw, ai_diagnosis, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, a.TenantID, a.Severity, a.RuleID, a.RuleName, a.MethodName,
		a.Pod, a.Namespace, a.Entity, a.PlainEnglish, a.PodCPUPct, a.PodMemMB,
		ts.Format(time.RFC3339), a.AmqpFrameRaw, a.AIDiagnosis, created.Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return id, err
}

// UpdateAlertDiagnosis updates the AI diagnosis field for an alert.
func (s *Store) UpdateAlertDiagnosis(ctx context.Context, tenantID string, id int64, diagnosis string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE alerts SET ai_diagnosis = ? WHERE tenant_id = ? AND id = ?
	`, diagnosis, tenantID, id)
	return err
}

// DeleteAllAlerts deletes all alerts for a tenant (used by handlePostAlert DELETE).
func (s *Store) DeleteAllAlerts(ctx context.Context, tenantID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM alerts WHERE tenant_id = ?`, tenantID)
	return err
}

// CountAlerts returns the total alert count for a tenant.
func (s *Store) CountAlerts(ctx context.Context, tenantID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM alerts WHERE tenant_id = ?`, tenantID).Scan(&count)
	return count, err
}

// scanAlerts scans multiple alert rows.
func scanAlerts(rows *sql.Rows) ([]*rules.Alert, error) {
	var alerts []*rules.Alert
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		alerts = append(alerts, a)
	}
	return alerts, rows.Err()
}

// scanAlert scans a single alert row from a *sql.Row or *sql.Rows.
func scanAlert(scanner interface{ Scan(...any) error }) (*rules.Alert, error) {
	var a rules.Alert
	var podCPU, podMem sql.NullFloat64
	var methodName, pod, namespace, entity, rawFrame, diagnosis sql.NullString
	var ts, created string

	err := scanner.Scan(
		&a.ID, &a.TenantID, &a.Severity, &a.RuleID, &a.RuleName,
		&methodName, &pod, &namespace, &entity, &a.PlainEnglish,
		&podCPU, &podMem, &ts, &rawFrame, &diagnosis, &created,
	)
	if err != nil {
		return nil, err
	}
	a.Timestamp = parseTimestamp(ts)
	a.CreatedAt = parseTimestamp(created)
	a.PodCPUPct = podCPU.Float64
	a.PodMemMB = podMem.Float64
	a.MethodName = methodName.String
	a.Pod = pod.String
	a.Namespace = namespace.String
	a.Entity = entity.String
	a.AmqpFrameRaw = rawFrame.String
	a.AIDiagnosis = diagnosis.String
	return &a, nil
}
