-- Migration: 001_init.sql
-- Full schema for SniffIt enterprise features
-- Timestamp defaults are handled in application code for SQLite/PostgreSQL portability.

-- Tenants table
CREATE TABLE IF NOT EXISTS tenants (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- API keys table (key_hash is SHA-256 of the plaintext bearer token)
CREATE TABLE IF NOT EXISTS api_keys (
    id TEXT PRIMARY KEY,
    key_hash TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    tenant_id TEXT NOT NULL DEFAULT 'default',
    rate_limit INTEGER NOT NULL DEFAULT 100,
    is_admin INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    last_used_at TEXT,
    expires_at TEXT,
    FOREIGN KEY (tenant_id) REFERENCES tenants(id)
);

-- Alerts table (tenant-scoped)
CREATE TABLE IF NOT EXISTS alerts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id TEXT NOT NULL DEFAULT 'default',
    severity TEXT NOT NULL,
    rule_id TEXT NOT NULL,
    rule_name TEXT NOT NULL,
    method_name TEXT,
    pod TEXT,
    namespace TEXT,
    entity TEXT,
    plain_english TEXT NOT NULL,
    pod_cpu_pct REAL,
    pod_mem_mb REAL,
    timestamp TEXT NOT NULL,
    amqp_frame_raw TEXT,
    ai_diagnosis TEXT,
    created_at TEXT NOT NULL,
    FOREIGN KEY (tenant_id) REFERENCES tenants(id)
);

CREATE INDEX IF NOT EXISTS idx_alerts_tenant_timestamp ON alerts(tenant_id, timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_alerts_severity ON alerts(tenant_id, severity);

-- Settings table (one per tenant)
CREATE TABLE IF NOT EXISTS settings (
    tenant_id TEXT PRIMARY KEY,
    ai_trigger_level TEXT NOT NULL DEFAULT 'error',
    slack_webhook_url TEXT,
    min_notify_sev TEXT NOT NULL DEFAULT 'warn',
    updated_at TEXT NOT NULL,
    FOREIGN KEY (tenant_id) REFERENCES tenants(id)
);

-- Audit log
CREATE TABLE IF NOT EXISTS audit_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    api_key_id TEXT,
    tenant_id TEXT,
    method TEXT NOT NULL,
    path TEXT NOT NULL,
    status_code INTEGER,
    ip_address TEXT,
    user_agent TEXT,
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_audit_tenant ON audit_log(tenant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_api_key ON audit_log(api_key_id);

-- Topology: queues (tenant-scoped)
CREATE TABLE IF NOT EXISTS topology_queues (
    tenant_id TEXT NOT NULL DEFAULT 'default',
    name TEXT NOT NULL,
    durable INTEGER NOT NULL DEFAULT 0,
    exclusive INTEGER NOT NULL DEFAULT 0,
    auto_delete INTEGER NOT NULL DEFAULT 0,
    first_seen TEXT NOT NULL,
    last_seen TEXT NOT NULL,
    active INTEGER NOT NULL DEFAULT 1,
    PRIMARY KEY (tenant_id, name),
    FOREIGN KEY (tenant_id) REFERENCES tenants(id)
);

-- Topology: exchanges
CREATE TABLE IF NOT EXISTS topology_exchanges (
    tenant_id TEXT NOT NULL DEFAULT 'default',
    name TEXT NOT NULL,
    ex_type TEXT NOT NULL,
    durable INTEGER NOT NULL DEFAULT 0,
    first_seen TEXT NOT NULL,
    active INTEGER NOT NULL DEFAULT 1,
    PRIMARY KEY (tenant_id, name),
    FOREIGN KEY (tenant_id) REFERENCES tenants(id)
);

-- Topology: consumers
CREATE TABLE IF NOT EXISTS topology_consumers (
    tenant_id TEXT NOT NULL DEFAULT 'default',
    consumer_tag TEXT NOT NULL,
    queue TEXT NOT NULL,
    exclusive INTEGER NOT NULL DEFAULT 0,
    first_seen TEXT NOT NULL,
    active INTEGER NOT NULL DEFAULT 1,
    PRIMARY KEY (tenant_id, consumer_tag),
    FOREIGN KEY (tenant_id) REFERENCES tenants(id)
);

-- Topology: connections
CREATE TABLE IF NOT EXISTS topology_connections (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id TEXT NOT NULL DEFAULT 'default',
    source TEXT NOT NULL,
    opened_at TEXT NOT NULL,
    closed_at TEXT,
    active INTEGER NOT NULL DEFAULT 1,
    FOREIGN KEY (tenant_id) REFERENCES tenants(id)
);

CREATE INDEX IF NOT EXISTS idx_connections_tenant ON topology_connections(tenant_id, opened_at DESC);

-- Schema migrations tracking
CREATE TABLE IF NOT EXISTS schema_migrations (
    name TEXT PRIMARY KEY,
    applied_at TEXT NOT NULL
);
