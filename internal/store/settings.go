package store

import (
	"context"
	"database/sql"
	"time"
)

// Settings mirrors the controlplane.Settings struct but persisted per-tenant.
type Settings struct {
	TenantID       string `json:"tenant_id"`
	AITriggerLevel string `json:"ai_trigger_level"`
	SlackWebhookURL string `json:"slack_webhook_url"`
	MinNotifySev   string `json:"min_notify_severity"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// GetSettings returns settings for a tenant, inserting defaults if absent.
func (s *Store) GetSettings(ctx context.Context, tenantID string) (*Settings, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT tenant_id, ai_trigger_level, slack_webhook_url, min_notify_sev, updated_at
		FROM settings WHERE tenant_id = ?
	`, tenantID)
	var st Settings
	var slackURL sql.NullString
	err := row.Scan(&st.TenantID, &st.AITriggerLevel, &slackURL, &st.MinNotifySev, &st.UpdatedAt)
	if err == sql.ErrNoRows {
		// Insert defaults and return them
		st = Settings{TenantID: tenantID, AITriggerLevel: "error", MinNotifySev: "warn"}
		_, _ = s.db.ExecContext(ctx, `
			INSERT INTO settings (tenant_id, ai_trigger_level, min_notify_sev) VALUES (?, ?, ?)
		`, tenantID, st.AITriggerLevel, st.MinNotifySev)
		return &st, nil
	}
	if err != nil {
		return nil, err
	}
	st.SlackWebhookURL = slackURL.String
	return &st, nil
}

// UpsertSettings updates or inserts settings for a tenant.
func (s *Store) UpsertSettings(ctx context.Context, st *Settings) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO settings (tenant_id, ai_trigger_level, slack_webhook_url, min_notify_sev, updated_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(tenant_id) DO UPDATE SET
			ai_trigger_level = EXCLUDED.ai_trigger_level,
			slack_webhook_url = EXCLUDED.slack_webhook_url,
			min_notify_sev = EXCLUDED.min_notify_sev,
			updated_at = CURRENT_TIMESTAMP
	`, st.TenantID, st.AITriggerLevel, st.SlackWebhookURL, st.MinNotifySev)
	return err
}
