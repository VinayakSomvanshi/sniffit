import { useEffect, useState, useCallback, useRef } from 'react';
import { Activity, CheckCircle2, TerminalSquare, Settings, RefreshCw, Trash2, ChevronRight, Circle, Search, Download, Database, Wifi, WifiOff, MessageSquare, Sparkles, Send } from 'lucide-react';

interface Alert {
  severity: "error" | "warn" | "info";
  rule_id: string;
  rule_name: string;
  method_name?: string;
  source?: string;
  broker?: string;
  entity?: string;
  layman: string;
  pod_cpu_pct: number;
  pod_mem_mb: number;
  timestamp: string;
  amqp_frame_raw: string;
  ai_diagnosis?: string;
  // legacy field mapping
  pod?: string;
  namespace?: string;
}

interface QueueInfo {
  name: string;
  durable: boolean;
  exclusive: boolean;
  auto_delete: boolean;
  first_seen: string;
  last_seen: string;
  active: boolean;
}

interface ExchangeInfo {
  name: string;
  type: string;
  durable: boolean;
  first_seen: string;
  active: boolean;
}

interface ConsumerInfo {
  consumer_tag: string;
  queue: string;
  exclusive: boolean;
  first_seen: string;
  active: boolean;
}

interface Topology {
  queues: Record<string, QueueInfo>;
  exchanges: Record<string, ExchangeInfo>;
  consumers: Record<string, ConsumerInfo>;
  last_updated: string;
}

interface AppSettings {
  ai_trigger_level: string;
  slack_webhook_url: string;
  min_notify_severity: string;
}

interface HealthSummary {
  score: number;
  status: string;
  summary: string;
  key_issues?: string[];
  generated_at: string;
  heuristic?: boolean;
}

interface StreamAnalysis {
  top_issues: string;
  patterns: string;
  root_cause: string;
  recommendations: string;
  event_count: number;
  generated_at: string;
}

interface ChatMessage {
  role: 'user' | 'assistant';
  content: string;
}


const RULES = [
  { id: '1.1', cat: '1', catLabel: 'Connection & Authentication', name: 'Authentication Refused', trigger: 'TCP handshake succeeds; AMQP connection drops with ACCESS_REFUSED', alert: "Authentication failed: invalid credentials, or vhost does not exist/lacks permissions.", severity: 'error' },
  { id: '1.2', cat: '1', catLabel: 'Connection & Authentication', name: 'Missed Heartbeats', trigger: 'Broker drops connection citing missed heartbeats', alert: "Heartbeat timeout: broker dropped the connection. Check CPU load or network partitions.", severity: 'error' },
  { id: '1.3', cat: '1', catLabel: 'Connection & Authentication', name: 'Connection Limit Exceeded', trigger: 'Broker forcefully closes new connections after limit is hit', alert: "Connection limit reached. Inspect the application for connection leaks.", severity: 'error' },
  { id: '1.4', cat: '1', catLabel: 'Connection & Authentication', name: 'Handshake Timeout Override', trigger: 'Server-side heartbeat clamping during Connection.Tune', alert: "Server clamped client-requested timeout value for stability.", severity: 'warn' },
  { id: '2.1', cat: '2', catLabel: 'Resource Constraints', name: 'Memory Watermark Alarm', trigger: 'connection.blocked due to memory', alert: "Publishing blocked (memory): broker breached memory threshold, blocking publishers.", severity: 'warn' },
  { id: '2.2', cat: '2', catLabel: 'Resource Constraints', name: 'Disk Watermark Alarm', trigger: 'connection.blocked due to disk', alert: "Publishing blocked (disk): broker node exhausted free disk space.", severity: 'warn' },
  { id: '2.3', cat: '2', catLabel: 'Resource Constraints', name: 'Channel Limit Exceeded', trigger: 'channel_error / NOT_ALLOWED when opening new channel', alert: "Channel exhaustion: more channels than channel_max permits. Check for channel leaks.", severity: 'error' },
  { id: '3.1', cat: '3', catLabel: 'Topology & Protocol', name: 'Topology Mismatch', trigger: 'channel.close with PRECONDITION_FAILED', alert: "Topology mismatch: entity declared with conflicting parameters.", severity: 'error' },
  { id: '3.2', cat: '3', catLabel: 'Topology & Protocol', name: 'Unroutable Message', trigger: 'Message published with mandatory flag; basic.return fired', alert: "Message dropped: mandatory-flagged message had no matching binding.", severity: 'warn' },
  { id: '3.3', cat: '3', catLabel: 'Topology & Protocol', name: 'Entity Not Found', trigger: 'channel.close with NOT_FOUND', alert: "Routing error: exchange or queue does not exist on the broker.", severity: 'error' },
  { id: '3.4', cat: '3', catLabel: 'Topology & Protocol', name: 'Frame Size Exceeded', trigger: 'FRAME_ERROR on channel or connection close', alert: "Payload exceeds broker max_frame_size limit.", severity: 'error' },
  { id: '4.1', cat: '4', catLabel: 'Consumer Failures', name: 'Consumer ACK Timeout', trigger: 'Connection drops: delivery acknowledgement timed out', alert: "Consumer timeout: took too long to acknowledge a message.", severity: 'error' },
  { id: '4.3', cat: '4', catLabel: 'Consumer Failures', name: 'Message Rejected', trigger: 'basic.reject received from consumer', alert: "Consumer explicitly rejected a delivery.", severity: 'warn' },
  { id: '4.4', cat: '4', catLabel: 'Consumer Failures', name: 'Message Nack', trigger: 'basic.nack received from consumer', alert: "Consumer negatively acknowledged; processing failure suspected.", severity: 'warn' },
  { id: '5.1', cat: '5', catLabel: 'Security', name: 'Management Auth Failure', trigger: 'HTTP 401/403 on port 15672', alert: "Unauthorized access attempt on RabbitMQ Management API.", severity: 'error' },
];

function timeAgo(isoStr: string) {
  const diff = Math.floor((Date.now() - new Date(isoStr).getTime()) / 1000);
  if (diff < 5) return 'just now';
  if (diff < 60) return `${diff}s ago`;
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
  return `${Math.floor(diff / 3600)}h ago`;
}

function fmtTime(isoStr: string) {
  if (!isoStr) return '---';
  try {
    return new Date(isoStr).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
  } catch { return isoStr; }
}

// Derive the source identifier for display — sniffer catches external traffic
function getSource(alert: Alert): string {
  return alert.source || alert.pod || 'sniffit-sidecar';
}

function getBroker(alert: Alert): string {
  return alert.broker || alert.namespace || 'localhost';
}

const SEV_COLOR: Record<string, string> = {
  error: 'var(--red)',
  warn: 'var(--yellow)',
  info: 'var(--blue)',
};

function SevBadge({ sev }: { sev: string }) {
  return (
    <span style={{
      fontSize: '10px', fontWeight: 700, textTransform: 'uppercase', letterSpacing: '0.05em',
      padding: '2px 7px', borderRadius: '3px',
      color: SEV_COLOR[sev] || 'var(--text-muted)',
      background: `${SEV_COLOR[sev] || 'var(--border)'}18`,
      border: `1px solid ${SEV_COLOR[sev] || 'var(--border)'}40`,
    }}>
      {sev}
    </span>
  );
}

function AlertRow({ alert, onClick, selected }: { alert: Alert; onClick: () => void; selected: boolean }) {
  return (
    <div
      onClick={onClick}
      className={`alert-row ${selected ? 'selected' : ''}`}
      style={{
        borderLeft: `3px solid ${SEV_COLOR[alert.severity] || 'transparent'}`,
      }}
    >
      <div style={{ display: 'flex', flexDirection: 'column', gap: '3px' }}>
        <SevBadge sev={alert.severity} />
        <span style={{ fontSize: '10px', fontFamily: 'var(--mono)', color: 'var(--text-dim)' }}>rule {alert.rule_id}</span>
      </div>
      <div>
        <div style={{ fontSize: '13px', fontWeight: 500, color: 'var(--text)', marginBottom: '2px', lineHeight: 1.3 }}>
          {alert.layman.slice(0, 100)}{alert.layman.length > 100 ? '...' : ''}
        </div>
        <div style={{ display: 'flex', gap: '12px', fontSize: '11px', color: 'var(--text-dim)', fontFamily: 'var(--mono)' }}>
          {alert.method_name && <span>{alert.method_name}</span>}
          {alert.entity && <span style={{ color: 'var(--text-muted)' }}>entity: {alert.entity}</span>}
        </div>
      </div>
      <div style={{ fontSize: '11px', color: 'var(--text-dim)', textAlign: 'right', whiteSpace: 'nowrap' }}>
        {timeAgo(alert.timestamp)}
      </div>
    </div>
  );
}

export default function App() {
  const [alerts, setAlerts] = useState<Alert[]>([]);
  const [connected, setConnected] = useState(false);
  const [activePage, setActivePage] = useState('dashboard');
  const [selectedAlert, setSelectedAlert] = useState<Alert | null>(null);
  const [aiLoading, setAiLoading] = useState(false);
  const [alertFilter, setAlertFilter] = useState('all');
  const [search, setSearch] = useState('');
  const [ruleSearch, setRuleSearch] = useState('');
  const [topology, setTopology] = useState<Topology | null>(null);
  const [topoLoading, setTopoLoading] = useState(false);

  const [showLogin, setShowLogin] = useState(false);
  const [authToken, setAuthToken] = useState(localStorage.getItem('sniffit_token') || '');
  const [apiKeyInput, setApiKeyInput] = useState('');
  const [isAdmin, setIsAdmin] = useState(localStorage.getItem('sniffit_is_admin') === 'true');
  const [tenantId, setTenantId] = useState(localStorage.getItem('sniffit_tenant_id') || '');
  console.log('Current tenant:', tenantId);

  const apiFetch = async (url: string, init?: RequestInit) => {
    const headers = new Headers(init?.headers);
    if (authToken) {
      headers.set('Authorization', `Bearer ${authToken}`);
    }
    const res = await fetch(url, { ...init, headers });
    if (res.status === 401) {
      setShowLogin(true);
      setAuthToken('');
      localStorage.removeItem('sniffit_token');
      localStorage.removeItem('sniffit_is_admin');
      localStorage.removeItem('sniffit_tenant_id');
      throw new Error('Unauthorized');
    }
    return res;
  };



  // Settings — fully controlled
  const [settings, setSettings] = useState<AppSettings>({
    ai_trigger_level: 'error',
    slack_webhook_url: '',
    min_notify_severity: 'warn',
  });
  const [settingsDirty, setSettingsDirty] = useState(false);
  const [settingsSaving, setSettingsSaving] = useState(false);
  const [settingsSaved, setSettingsSaved] = useState(false);

  // AI capabilities state
  const [health, setHealth] = useState<HealthSummary | null>(null);
  const [healthLoading, setHealthLoading] = useState(false);
  const [streamAnalysis, setStreamAnalysis] = useState<StreamAnalysis | null>(null);
  const [streamLoading, setStreamLoading] = useState(false);
  const [chatHistory, setChatHistory] = useState<ChatMessage[]>([]);
  const [chatInput, setChatInput] = useState('');
  const [chatLoading, setChatLoading] = useState(false);
  const chatEndRef = useRef<HTMLDivElement>(null);

  const updateSetting = <K extends keyof AppSettings>(k: K, v: AppSettings[K]) => {
    setSettings(prev => ({ ...prev, [k]: v }));
    setSettingsDirty(true);
    setSettingsSaved(false);
  };

  const saveSettings = async () => {
    setSettingsSaving(true);
    try {
      await apiFetch('/api/settings', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(settings),
      });
      setSettingsDirty(false);
      setSettingsSaved(true);
      setTimeout(() => setSettingsSaved(false), 3000);
    } catch (e) {
      console.error('Failed to save settings:', e);
    } finally {
      setSettingsSaving(false);
    }
  };

  const loadTopology = useCallback(async () => {
    setTopoLoading(true);
    try {
      const res = await apiFetch('/api/topology');
      const data = await res.json();
      setTopology(data);
    } catch (e) {
      console.error('Topology fetch failed:', e);
    } finally {
      setTopoLoading(false);
    }
  }, []);

  const loadHealth = useCallback(async () => {
    setHealthLoading(true);
    try {
      const res = await apiFetch('/api/ai/health');
      if (res.ok) setHealth(await res.json());
    } catch (e) {
      console.error('Health fetch failed:', e);
    } finally {
      setHealthLoading(false);
    }
  }, []);

  const loadStreamAnalysis = async () => {
    setStreamLoading(true);
    try {
      const res = await apiFetch('/api/ai/stream');
      if (res.ok) setStreamAnalysis(await res.json());
    } catch (e) {
      console.error('Stream analysis failed:', e);
    } finally {
      setStreamLoading(false);
    }
  };

  const handleChatSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!chatInput.trim() || chatLoading) return;
    const msg = chatInput.trim();
    setChatInput('');
    setChatHistory(prev => [...prev, { role: 'user', content: msg }]);
    setChatLoading(true);

    try {
      const res = await apiFetch('/api/ai/chat', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ message: msg, history: chatHistory }),
      });
      const data = await res.json();
      if (data.reply) {
        setChatHistory(prev => [...prev, { role: 'assistant', content: data.reply }]);
      }
    } catch (err) {
      console.error('Chat failed:', err);
      setChatHistory(prev => [...prev, { role: 'assistant', content: 'Connection error. Please try again.' }]);
    } finally {
      setChatLoading(false);
    }
  };

  useEffect(() => {
    chatEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [chatHistory, chatLoading]);

  useEffect(() => {
    // 1. Load alert history
    apiFetch('/api/alerts/history')
      .then(r => r.json())
      .then(data => { if (Array.isArray(data)) setAlerts(data.reverse().slice(0, 500)); })
      .catch(console.error);

    // 2. Load current settings
    apiFetch('/api/settings')
      .then(r => r.json())
      .then(data => { if (data) setSettings(s => ({ ...s, ...data })); })
      .catch(console.error);

    // 3. SSE for real-time events
    const tokenParam = authToken ? `?token=${authToken}` : '';
    const evtSource = new EventSource('/api/events' + tokenParam);
    evtSource.onopen = () => setConnected(true);
    evtSource.onerror = () => setConnected(false);
    evtSource.onmessage = (event) => {
      try {
        const parsed: Alert = JSON.parse(event.data);
        setAlerts(prev => {
          const idx = prev.findIndex(a =>
            a.timestamp === parsed.timestamp && a.rule_id === parsed.rule_id
          );
          if (idx >= 0) {
            const next = [...prev];
            next[idx] = parsed;
            setSelectedAlert(curr => (curr?.timestamp === parsed.timestamp && curr?.rule_id === parsed.rule_id) ? parsed : curr);
            return next;
          }
          return [parsed, ...prev].slice(0, 500);
        });
      } catch { /* ignore parse errors from ping events */ }
    };

    return () => evtSource.close();
  }, []);

  // Auto-refresh topology when on topology page
  useEffect(() => {
    if (activePage === 'topology') {
      loadTopology();
    }
    if (activePage === 'dashboard') {
      loadHealth();
    }
  }, [activePage, loadTopology, loadHealth]);

  const handleClearAlerts = async () => {
    if (!window.confirm('Clear all alert history and topology? This cannot be undone.')) return;
    try {
      const res = await apiFetch('/api/alerts', { method: 'DELETE' });
      if (res.ok) {
        setAlerts([]);
        setSelectedAlert(null);
        setTopology(null);
      }
    } catch (e) { console.error(e); }
  };

  const handleAskAI = async () => {
    if (!selectedAlert) return;
    setAiLoading(true);
    try {
      const res = await apiFetch('/api/analyze', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(selectedAlert),
      });
      const data = await res.json();
      if (data.diagnosis) {
        const updated = { ...selectedAlert, ai_diagnosis: data.diagnosis };
        setSelectedAlert(updated);
        setAlerts(prev => prev.map(a =>
          a.timestamp === selectedAlert.timestamp && a.rule_id === selectedAlert.rule_id ? updated : a
        ));
      }
    } catch (e) { console.error(e); alert('AI analysis failed'); }
    finally { setAiLoading(false); }
  };

  // Derived counts
  const errCount = alerts.filter(a => a.severity === 'error').length;
  const warnCount = alerts.filter(a => a.severity === 'warn').length;
  const infoCount = alerts.filter(a => a.severity === 'info').length;

  let filtered = [...alerts];
  if (alertFilter !== 'all') {
    if (['error', 'warn', 'info'].includes(alertFilter)) {
      filtered = filtered.filter(a => a.severity === alertFilter);
    } else {
      filtered = filtered.filter(a => String(a.rule_id).startsWith(alertFilter));
    }
  }
  if (search) {
    const q = search.toLowerCase();
    filtered = filtered.filter(a =>
      (a.layman || '').toLowerCase().includes(q) ||
      (a.rule_name || '').toLowerCase().includes(q) ||
      (a.method_name || '').toLowerCase().includes(q) ||
      (a.entity || '').toLowerCase().includes(q) ||
      (a.rule_id || '').includes(q)
    );
  }

  // 5-minute bar chart buckets (last 1hr)
  const now = Date.now();
  const bucketMs = 5 * 60 * 1000;
  const barData = Array.from({ length: 12 }).map((_, i) => {
    const start = now - (12 - i) * bucketMs;
    const end = start + bucketMs;
    const bucket = alerts.filter(a => {
      const ts = new Date(a.timestamp).getTime();
      return ts >= start && ts < end;
    });
    const e = bucket.filter(a => a.severity === 'error').length;
    const w = bucket.filter(a => a.severity === 'warn').length;
    const inf = bucket.filter(a => a.severity === 'info').length;
    return { e, w, inf, total: e + w + inf };
  });
  const maxBar = Math.max(1, ...barData.map(b => b.total));

  const queues = topology ? Object.values(topology.queues) : [];
  const exchanges = topology ? Object.values(topology.exchanges) : [];
  const consumers = topology ? Object.values(topology.consumers) : [];

  return (
    <>
      {/* -- Top Nav --------------------------------------------------─ */}
      <nav className="topnav">
        <div className="topnav-brand" onClick={() => setActivePage('dashboard')}>
          <Activity size={16} color="var(--accent)" />
          SniffIt
        </div>
        <div className="topnav-tabs">
          {(['dashboard', 'events', 'rules', 'topology', 'ai', 'settings', ...(isAdmin ? ['admin'] : [])] as const).map(p => (
            <button
              key={p}
              className={`topnav-tab ${activePage === p ? 'active' : ''}`}
            onClick={() => setActivePage(p)}
            >
              {p === 'ai' ? 'AI Hub' : p.charAt(0).toUpperCase() + p.slice(1)}
              {p === 'events' && alerts.length > 0 && (
                <span className="nav-badge">{alerts.length}</span>
              )}
            </button>
          ))}
        </div>
        <div className="topnav-right">
          <div className={`conn-dot ${connected ? 'on' : 'off'}`} title={connected ? 'SSE connected' : 'SSE disconnected'}>
            {connected ? <Wifi size={13} /> : <WifiOff size={13} />}
          </div>
          <button className="btn btn-ghost" onClick={handleClearAlerts} title="Clear all history">
            <Trash2 size={13} /> Clear
          </button>
          <button
            className="btn btn-primary"
            onClick={() => {
              const blob = new Blob([JSON.stringify(alerts, null, 2)], { type: 'application/json' });
              const a = document.createElement('a');
              a.href = URL.createObjectURL(blob);
              a.download = `sniffit-alerts-${Date.now()}.json`;
              a.click();
            }}
          >
            <Download size={13} /> Export
          </button>
        </div>
      </nav>

      {/* -- Main Body ------------------------------------------------─ */}
      <div className="shell">

        {/* DASHBOARD */}
        {activePage === 'dashboard' && (
          <div className="page">
            <div className="page-header">
              <div>
                <h1 style={{ fontSize: '20px', fontWeight: 600, color: '#fff', margin: 0 }}>Overview</h1>
                <p style={{ margin: 0, fontSize: '13px', color: 'var(--text-muted)', marginTop: '3px' }}>Real-time AMQP observability - last 60 minutes</p>
              </div>
            </div>

            {/* AI Health Overview */}
            {healthLoading ? (
              <div className="card" style={{ marginBottom: '24px', padding: '16px', color: 'var(--text-dim)', display: 'flex', alignItems: 'center', gap: '8px' }}>
                 <RefreshCw size={14} className="spin" /> Analyzing broker health...
              </div>
            ) : health && (
              <div className="card" style={{ marginBottom: '24px', borderLeft: `4px solid ${health.status === 'healthy' ? 'var(--green)' : health.status === 'warning' ? 'var(--yellow)' : 'var(--red)'}` }}>
                <div className="card-header" style={{ borderBottom: 'none', paddingBottom: 0 }}>
                  <div className="card-title" style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
                    <Sparkles size={16} color="var(--accent)" /> AI Health Synthesis
                    {health.heuristic && <span className="nav-badge" style={{ background: 'var(--surface2)', color: 'var(--text-dim)' }}>Heuristic Fallback</span>}
                  </div>
                  <div style={{ fontSize: '28px', fontWeight: 800, color: health.status === 'healthy' ? 'var(--green)' : health.status === 'warning' ? 'var(--yellow)' : 'var(--red)' }}>
                    {health.score}/100
                  </div>
                </div>
                <div className="card-body">
                  <p style={{ fontSize: '14px', lineHeight: 1.5, color: 'var(--text)', marginBottom: health.key_issues?.length ? '12px' : '0' }}>{health.summary}</p>
                  {health.key_issues && health.key_issues.length > 0 && (
                    <ul style={{ paddingLeft: '20px', margin: 0, fontSize: '13px', color: 'var(--text-muted)' }}>
                      {health.key_issues.map((iss, i) => <li key={i}>{iss}</li>)}
                    </ul>
                  )}
                </div>
              </div>
            )}

            {/* Stat Cards */}
            <div className="stat-row">
              <div className="stat-card" onClick={() => { setAlertFilter('error'); setActivePage('events'); }}>
                <div className="stat-label">Errors</div>
                <div className="stat-num" style={{ color: 'var(--red)' }}>{errCount}</div>
                <div className="stat-sub">rule violations</div>
              </div>
              <div className="stat-card" onClick={() => { setAlertFilter('warn'); setActivePage('events'); }}>
                <div className="stat-label">Warnings</div>
                <div className="stat-num" style={{ color: 'var(--yellow)' }}>{warnCount}</div>
                <div className="stat-sub">worth investigating</div>
              </div>
              <div className="stat-card" onClick={() => { setAlertFilter('info'); setActivePage('events'); }}>
                <div className="stat-label">Info Events</div>
                <div className="stat-num" style={{ color: 'var(--blue)' }}>{infoCount}</div>
                <div className="stat-sub">lifecycle & topology</div>
              </div>
              <div className="stat-card" onClick={() => setActivePage('topology')}>
                <div className="stat-label">Topology</div>
                <div className="stat-num" style={{ color: 'var(--accent)' }}>{queues.filter(q => q.active).length}q - {exchanges.filter(e => e.active).length}x</div>
                <div className="stat-sub">queues - exchanges</div>
              </div>
            </div>

            {/* Alert volume chart */}
            <div className="card" style={{ marginBottom: '24px' }}>
              <div className="card-header">
                <div className="card-title">Alert Volume - last 60 min (5-min buckets)</div>
              </div>
              <div className="card-body">
                <div style={{ display: 'flex', alignItems: 'flex-end', gap: '4px', height: '80px' }}>
                  {barData.map((b, i) => (
                    <div key={i} style={{ flex: 1, display: 'flex', flexDirection: 'column', gap: '1px', alignItems: 'stretch', height: '100%', justifyContent: 'flex-end' }} title={`${b.e}E / ${b.w}W / ${b.inf}I`}>
                      {b.e > 0 && <div style={{ background: 'var(--red)', flex: b.e / maxBar, opacity: 0.85, borderRadius: '2px 2px 0 0', minHeight: '3px' }} />}
                      {b.w > 0 && <div style={{ background: 'var(--yellow)', flex: b.w / maxBar, opacity: 0.85, minHeight: '3px' }} />}
                      {b.inf > 0 && <div style={{ background: 'var(--blue)', flex: b.inf / maxBar, opacity: 0.5, minHeight: '3px' }} />}
                    </div>
                  ))}
                </div>
                <div style={{ display: 'flex', gap: '16px', marginTop: '8px', fontSize: '11px', color: 'var(--text-dim)' }}>
                  <span style={{ color: 'var(--red)' }}>[#]</span> Error
                  <span style={{ color: 'var(--yellow)' }}>[#]</span> Warn
                  <span style={{ color: 'var(--blue)' }}>[#]</span> Info
                </div>
              </div>
            </div>

            {/* Recent events table */}
            <div className="card">
              <div className="card-header">
                <div className="card-title">Recent Events</div>
                <button className="btn btn-ghost" style={{ fontSize: '12px' }} onClick={() => setActivePage('events')}>
                  View all <ChevronRight size={12} />
                </button>
              </div>
              <div>
                {alerts.slice(0, 8).length === 0 ? (
                  <div className="empty-state">
                    <Circle size={24} color="var(--text-dim)" />
                    <p>No events captured yet. Start the probe to begin observing traffic.</p>
                  </div>
                ) : alerts.slice(0, 8).map((a, i) => (
                  <AlertRow key={i} alert={a} selected={selectedAlert?.timestamp === a.timestamp} onClick={() => setSelectedAlert(a)} />
                ))}
              </div>
            </div>
          </div>
        )}

        {/* EVENTS FEED */}
        {activePage === 'events' && (
          <div className="page">
            <div className="page-header">
              <div>
                <h1 style={{ fontSize: '20px', fontWeight: 600, color: '#fff', margin: 0 }}>Events</h1>
                <p style={{ margin: 0, fontSize: '13px', color: 'var(--text-muted)', marginTop: '3px' }}>{alerts.length} total - {filtered.length} shown</p>
              </div>
            </div>

            {/* Filter bar */}
            <div className="filter-bar">
              {(['all', 'error', 'warn', 'info'] as const).map(f => (
                <button
                  key={f}
                  className={`filter-btn ${alertFilter === f ? 'active' : ''}`}
                  style={alertFilter === f && ['error', 'warn', 'info'].includes(f) ? { borderColor: SEV_COLOR[f], color: SEV_COLOR[f] } : {}}
                  onClick={() => setAlertFilter(f)}
                >
                  {f === 'all' ? 'All' : f.charAt(0).toUpperCase() + f.slice(1)}
                  <span className="filter-count">{
                    f === 'all' ? alerts.length :
                    f === 'error' ? errCount :
                    f === 'warn' ? warnCount : infoCount
                  }</span>
                </button>
              ))}
              <div style={{ width: '1px', height: '20px', background: 'var(--border)', margin: '0 4px' }} />
              {['1', '2', '3', '4', '5'].map(cat => (
                <button key={cat} className={`filter-btn ${alertFilter === cat ? 'active' : ''}`} onClick={() => setAlertFilter(cat)}>
                  Cat {cat}
                </button>
              ))}
              <div style={{ flex: 1 }} />
              <div className="search-wrap">
                <Search size={13} color="var(--text-dim)" />
                <input
                  className="search-input"
                  placeholder="Search events…"
                  value={search}
                  onChange={e => setSearch(e.target.value)}
                />
              </div>
            </div>

            <div className="card" style={{ padding: 0, overflow: 'hidden' }}>
              {/* Table header */}
              <div style={{
                display: 'grid', gridTemplateColumns: '80px 1fr auto',
                padding: '8px 16px', borderBottom: '1px solid var(--border)',
                fontSize: '11px', textTransform: 'uppercase', letterSpacing: '0.05em',
                color: 'var(--text-dim)', fontWeight: 600,
              }}>
                <span>Severity</span><span>Description</span><span>Time</span>
              </div>
              {filtered.length === 0 ? (
                <div className="empty-state"><Search size={24} color="var(--text-dim)" /><p>No events match your filter</p></div>
              ) : (
                filtered.map((a, i) => (
                  <AlertRow key={i} alert={a} selected={selectedAlert?.timestamp === a.timestamp && selectedAlert?.rule_id === a.rule_id} onClick={() => setSelectedAlert(a)} />
                ))
              )}
            </div>
          </div>
        )}

        {/* RULES */}
        {activePage === 'rules' && (
          <div className="page">
            <div className="page-header">
              <div>
                <h1 style={{ fontSize: '20px', fontWeight: 600, color: '#fff', margin: 0 }}>Detection Rules</h1>
                <p style={{ margin: 0, fontSize: '13px', color: 'var(--text-muted)', marginTop: '3px' }}>{RULES.length} heuristics active - AMQP 0-9-1 protocol</p>
              </div>
              <div className="search-wrap">
                <Search size={13} color="var(--text-dim)" />
                <input className="search-input" placeholder="Search rules…" value={ruleSearch} onChange={e => setRuleSearch(e.target.value)} />
              </div>
            </div>

            {['1', '2', '3', '4', '5'].map(cat => {
              const q = ruleSearch.toLowerCase();
              const catRules = RULES.filter(r =>
                r.cat === cat &&
                (!q || r.name.toLowerCase().includes(q) || r.id.includes(q) || r.trigger.toLowerCase().includes(q))
              );
              if (catRules.length === 0 && ruleSearch) return null;
              const catLabel = catRules[0]?.catLabel || `Category ${cat}`;
              return (
                <div key={cat} style={{ marginBottom: '32px' }}>
                  <div className="cat-label">{catLabel}</div>
                  <div className="rules-grid">
                    {catRules.map(rule => (
                      <div key={rule.id} className="rule-card">
                        <div style={{ display: 'flex', alignItems: 'center', gap: '10px', marginBottom: '10px' }}>
                          <span className="mono-tag">Rule {rule.id}</span>
                          <span style={{ flex: 1, fontWeight: 600, fontSize: '14px', color: '#fff' }}>{rule.name}</span>
                          <SevBadge sev={rule.severity} />
                        </div>
                        <p style={{ fontSize: '13px', color: 'var(--text-muted)', marginBottom: '12px', lineHeight: 1.5, flex: 1 }}>{rule.alert}</p>
                        <div className="trigger-block">
                          <TerminalSquare size={12} color="var(--text-dim)" />
                          {rule.trigger}
                        </div>
                        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginTop: '12px', paddingTop: '12px', borderTop: '1px solid var(--border)' }}>
                          <span style={{ fontSize: '12px', color: 'var(--accent)', display: 'flex', alignItems: 'center', gap: '5px' }}>
                            <CheckCircle2 size={12} /> Active
                          </span>
                          <span style={{ fontSize: '12px', color: 'var(--text-dim)' }}>
                            {alerts.filter(a => a.rule_id === rule.id).length} hits
                          </span>
                        </div>
                      </div>
                    ))}
                  </div>
                </div>
              );
            })}
          </div>
        )}

        {/* TOPOLOGY */}
        {activePage === 'topology' && (
          <div className="page">
            <div className="page-header">
              <div>
                <h1 style={{ fontSize: '20px', fontWeight: 600, color: '#fff', margin: 0 }}>Topology</h1>
                <p style={{ margin: 0, fontSize: '13px', color: 'var(--text-muted)', marginTop: '3px' }}>
                  Derived from captured AMQP traffic
                  {topology?.last_updated && ` - updated ${fmtTime(topology.last_updated)}`}
                </p>
              </div>
              <button className="btn btn-ghost" onClick={loadTopology} disabled={topoLoading}>
                <RefreshCw size={13} className={topoLoading ? 'spin' : ''} /> Refresh
              </button>
            </div>

            {(!topology || (queues.length + exchanges.length + consumers.length === 0)) ? (
              <div className="empty-state" style={{ marginTop: '40px' }}>
                <Database size={32} color="var(--text-dim)" />
                <p>No topology data yet.</p>
                <p style={{ fontSize: '12px', color: 'var(--text-dim)', marginTop: '4px' }}>
                  Topology is derived automatically from captured queue.declare, exchange.declare, and basic.consume frames.
                  Start the sniffer probe and generate some AMQP traffic.
                </p>
              </div>
            ) : (
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '16px' }}>

                {/* Queues */}
                <div className="card">
                  <div className="card-header">
                    <div className="card-title">Queues <span className="mono-tag" style={{ marginLeft: 8 }}>{queues.length}</span></div>
                  </div>
                  <div>
                    {queues.length === 0 ? (
                      <div style={{ padding: '16px', color: 'var(--text-dim)', fontSize: '13px' }}>No queues observed</div>
                    ) : queues.map(q => (
                      <div key={q.name} style={{ display: 'flex', alignItems: 'center', gap: '12px', padding: '10px 16px', borderBottom: '1px solid var(--border)', fontSize: '13px' }}>
                        <div style={{ width: '8px', height: '8px', borderRadius: '50%', background: q.active ? 'var(--green)' : 'var(--text-dim)', flexShrink: 0 }} />
                        <span style={{ flex: 1, fontFamily: 'var(--mono)', color: 'var(--text)', fontWeight: 500 }}>{q.name}</span>
                        <div style={{ display: 'flex', gap: '6px', fontSize: '11px' }}>
                          {q.durable && <span className="tag-pill">durable</span>}
                          {q.exclusive && <span className="tag-pill">exclusive</span>}
                          {q.auto_delete && <span className="tag-pill warn">auto-del</span>}
                        </div>
                        <span style={{ fontSize: '11px', color: 'var(--text-dim)' }}>{fmtTime(q.first_seen)}</span>
                      </div>
                    ))}
                  </div>
                </div>

                {/* Exchanges */}
                <div className="card">
                  <div className="card-header">
                    <div className="card-title">Exchanges <span className="mono-tag" style={{ marginLeft: 8 }}>{exchanges.length}</span></div>
                  </div>
                  <div>
                    {exchanges.length === 0 ? (
                      <div style={{ padding: '16px', color: 'var(--text-dim)', fontSize: '13px' }}>No exchanges observed</div>
                    ) : exchanges.map(ex => (
                      <div key={ex.name} style={{ display: 'flex', alignItems: 'center', gap: '12px', padding: '10px 16px', borderBottom: '1px solid var(--border)', fontSize: '13px' }}>
                        <div style={{ width: '8px', height: '8px', borderRadius: '50%', background: ex.active ? 'var(--green)' : 'var(--text-dim)', flexShrink: 0 }} />
                        <span style={{ flex: 1, fontFamily: 'var(--mono)', color: 'var(--text)', fontWeight: 500 }}>{ex.name || '(default)'}</span>
                        <span className="tag-pill">{ex.type || 'direct'}</span>
                        {ex.durable && <span className="tag-pill">durable</span>}
                        <span style={{ fontSize: '11px', color: 'var(--text-dim)' }}>{fmtTime(ex.first_seen)}</span>
                      </div>
                    ))}
                  </div>
                </div>

                {/* Consumers */}
                <div className="card">
                  <div className="card-header">
                    <div className="card-title">Consumers <span className="mono-tag" style={{ marginLeft: 8 }}>{consumers.filter(c => c.active).length} active</span></div>
                  </div>
                  <div>
                    {consumers.length === 0 ? (
                      <div style={{ padding: '16px', color: 'var(--text-dim)', fontSize: '13px' }}>No consumers observed</div>
                    ) : consumers.map(c => (
                      <div key={c.consumer_tag} style={{ display: 'flex', alignItems: 'center', gap: '12px', padding: '10px 16px', borderBottom: '1px solid var(--border)', fontSize: '13px' }}>
                        <div style={{ width: '8px', height: '8px', borderRadius: '50%', background: c.active ? 'var(--accent)' : 'var(--text-dim)', flexShrink: 0 }} />
                        <div style={{ flex: 1, minWidth: 0 }}>
                          <div style={{ fontFamily: 'var(--mono)', color: 'var(--text)', fontSize: '12px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{c.consumer_tag}</div>
                          <div style={{ fontSize: '11px', color: 'var(--text-dim)' }}>on {c.queue}</div>
                        </div>
                        {c.exclusive && <span className="tag-pill">exclusive</span>}
                        <span style={{ fontSize: '11px', color: 'var(--text-dim)' }}>{fmtTime(c.first_seen)}</span>
                      </div>
                    ))}
                  </div>
                </div>

                {/* Summary stats */}
                <div className="card">
                  <div className="card-header"><div className="card-title">Capture Stats</div></div>
                  <div style={{ padding: '16px', display: 'flex', flexDirection: 'column', gap: '10px' }}>
                    {[
                      ['Total Events', alerts.length],
                      ['Errors', errCount],
                      ['Warnings', warnCount],
                      ['Info Events', infoCount],
                      ['Active Queues', queues.filter(q => q.active).length],
                      ['Active Exchanges', exchanges.filter(e => e.active).length],
                      ['Active Consumers', consumers.filter(c => c.active).length],
                    ].map(([k, v]) => (
                      <div key={String(k)} style={{ display: 'flex', justifyContent: 'space-between', fontSize: '13px' }}>
                        <span style={{ color: 'var(--text-muted)' }}>{k}</span>
                        <span style={{ fontFamily: 'var(--mono)', color: 'var(--text)', fontWeight: 600 }}>{v}</span>
                      </div>
                    ))}
                  </div>
                </div>
              </div>
            )}
          </div>
        )}

        {/* AI HUB */}
        {activePage === 'ai' && (
          <div className="page" style={{ display: 'flex', flexDirection: 'column', height: 'calc(100vh - 48px)', padding: '24px', maxWidth: 'none', margin: '0' }}>
            <div className="page-header" style={{ marginBottom: '16px', flexShrink: 0 }}>
              <div>
                <h1 style={{ fontSize: '20px', fontWeight: 600, color: '#fff', margin: 0 }}>
                  <Sparkles size={18} color="var(--accent)" style={{ marginRight: '8px', verticalAlign: '-3px' }}/> AI Diagnostic Hub
                </h1>
                <p style={{ margin: 0, fontSize: '13px', color: 'var(--text-muted)', marginTop: '3px' }}>Ask questions and analyze patterns using embedded GenAI.</p>
              </div>
            </div>

            <div style={{ display: 'flex', gap: '20px', flex: 1, minHeight: 0 }}>
              {/* Left Panel: Stream Analysis */}
              <div className="card" style={{ flex: 1, display: 'flex', flexDirection: 'column', minHeight: 0 }}>
                <div className="card-header" style={{ flexShrink: 0 }}>
                  <div className="card-title">Event Stream Analysis</div>
                  <button className="btn btn-primary" style={{ fontSize: '12px' }} onClick={loadStreamAnalysis} disabled={streamLoading}>
                    {streamLoading ? <RefreshCw size={12} className="spin" /> : <Sparkles size={12} />} 
                    {streamAnalysis ? 'Refresh Analysis' : 'Run Full Analysis'}
                  </button>
                </div>
                <div className="card-body" style={{ overflowY: 'auto', flex: 1 }}>
                  {!streamAnalysis && !streamLoading ? (
                    <div className="empty-state">
                      <MessageSquare size={24} color="var(--text-dim)" />
                      <p>Run a stream analysis to automatically correlate anomalies and identify multi-event failure patterns.</p>
                    </div>
                  ) : streamLoading ? (
                     <div className="empty-state">
                      <RefreshCw size={24} className="spin" color="var(--accent)" />
                      <p>Analyzing event stream across AI models...</p>
                    </div>
                  ) : streamAnalysis ? (
                    <div style={{ display: 'flex', flexDirection: 'column', gap: '20px' }}>
                       <div>
                         <div className="cat-label">Top Issues</div>
                         <div style={{ color: 'var(--text)', fontSize: '13px', whiteSpace: 'pre-wrap', lineHeight: 1.6 }}>{streamAnalysis.top_issues}</div>
                       </div>
                       <div>
                         <div className="cat-label">Patterns & Correlation</div>
                         <div style={{ color: 'var(--text)', fontSize: '13px', whiteSpace: 'pre-wrap', lineHeight: 1.6 }}>{streamAnalysis.patterns}</div>
                       </div>
                       <div style={{ background: 'var(--surface2)', border: '1px solid var(--border)', borderRadius: '6px', padding: '12px' }}>
                         <div className="cat-label" style={{ border: 'none', marginBottom: '4px', padding: 0 }}>Root Cause</div>
                         <div style={{ color: 'var(--red)', fontSize: '13px', whiteSpace: 'pre-wrap', lineHeight: 1.6, fontWeight: 500 }}>{streamAnalysis.root_cause}</div>
                       </div>
                       <div>
                         <div className="cat-label">Recommendations</div>
                         <div style={{ color: 'var(--accent)', fontSize: '13px', whiteSpace: 'pre-wrap', lineHeight: 1.6 }}>{streamAnalysis.recommendations}</div>
                       </div>
                    </div>
                  ) : null}
                </div>
              </div>

              {/* Right Panel: Chat */}
              <div className="card" style={{ flex: 1, display: 'flex', flexDirection: 'column', minHeight: 0 }}>
                 <div className="card-header" style={{ flexShrink: 0 }}>
                  <div className="card-title">Event Context Chat</div>
                </div>
                <div className="card-body" style={{ flex: 1, overflowY: 'auto', display: 'flex', flexDirection: 'column', gap: '12px', paddingBottom: '20px' }}>
                  {chatHistory.length === 0 ? (
                    <div className="empty-state" style={{ marginTop: 'auto', marginBottom: 'auto' }}>
                      <p>Ask anything about why an event happened, broker config, or how to fix current issues.</p>
                    </div>
                  ) : (
                    chatHistory.map((msg, i) => (
                      <div key={i} style={{
                        alignSelf: msg.role === 'user' ? 'flex-end' : 'flex-start',
                        maxWidth: '85%',
                        padding: '10px 14px',
                        borderRadius: '8px',
                        background: msg.role === 'user' ? 'var(--accent)' : 'var(--surface2)',
                        border: msg.role === 'user' ? 'none' : '1px solid var(--border)',
                        color: msg.role === 'user' ? '#fff' : 'var(--text)',
                        fontSize: '13px',
                        lineHeight: 1.5,
                        whiteSpace: 'pre-wrap'
                      }}>
                        {msg.content}
                      </div>
                    ))
                  )}
                  {chatLoading && (
                    <div style={{ alignSelf: 'flex-start', padding: '10px 14px', borderRadius: '8px', background: 'var(--surface2)', border: '1px solid var(--border)', color: 'var(--text-dim)', fontSize: '13px' }}>
                      <span className="spin" style={{ display: 'inline-block', marginRight: '6px' }}>*</span> Thinking...
                    </div>
                  )}
                  <div ref={chatEndRef} />
                </div>
                <div style={{ padding: '12px', borderTop: '1px solid var(--border)', background: 'var(--surface)', flexShrink: 0 }}>
                  <form onSubmit={handleChatSubmit} style={{ display: 'flex', gap: '8px' }}>
                    <input 
                      type="text" 
                      className="text-input" 
                      style={{ flex: 1, width: 'auto' }} 
                      placeholder="Ask about your RabbitMQ traffic..." 
                      value={chatInput} 
                      onChange={e => setChatInput(e.target.value)} 
                      disabled={chatLoading}
                    />
                    <button type="submit" className="btn btn-primary" disabled={!chatInput.trim() || chatLoading}>
                      <Send size={14} /> 
                    </button>
                  </form>
                </div>
              </div>
            </div>
          </div>
        )}

        {/* SETTINGS */}
        {activePage === 'settings' && (
          <div className="page">
            <div className="page-header">
              <div>
                <h1 style={{ fontSize: '20px', fontWeight: 600, color: '#fff', margin: 0 }}>Settings</h1>
                <p style={{ margin: 0, fontSize: '13px', color: 'var(--text-muted)', marginTop: '3px' }}>Persisted to disk — survive restarts</p>
              </div>
              <div style={{ display: 'flex', alignItems: 'center', gap: '12px' }}>
                {settingsSaved && <span style={{ fontSize: '12px', color: 'var(--green)' }}>OK Saved</span>}
                {settingsDirty && !settingsSaved && <span style={{ fontSize: '12px', color: 'var(--yellow)' }}>Unsaved changes</span>}
                <button
                  className="btn btn-primary"
                  onClick={saveSettings}
                  disabled={settingsSaving || !settingsDirty}
                >
                  <Settings size={13} />
                  {settingsSaving ? 'Saving…' : 'Save Settings'}
                </button>
              </div>
            </div>

            <div style={{ maxWidth: '680px', display: 'flex', flexDirection: 'column', gap: '16px' }}>

              {/* AI Settings */}
              <div className="card">
                <div className="card-header"><div className="card-title">AI Diagnosis</div></div>
                <div className="card-body" style={{ display: 'flex', flexDirection: 'column', gap: '20px' }}>
                  <div className="setting-row">
                    <div>
                      <div className="setting-label">AI Trigger Threshold</div>
                      <div className="setting-desc">Minimum severity to automatically run AI diagnosis on incoming alerts</div>
                    </div>
                    <select
                      className="select-input"
                      value={settings.ai_trigger_level}
                      onChange={e => updateSetting('ai_trigger_level', e.target.value)}
                    >
                      <option value="error">Errors only</option>
                      <option value="warn">Warnings + Errors</option>
                      <option value="info">All events (Info+)</option>
                      <option value="all">All (Full Auto)</option>
                    </select>
                  </div>
                </div>
              </div>

              {/* Notification Settings */}
              <div className="card">
                <div className="card-header"><div className="card-title">Notifications</div></div>
                <div className="card-body" style={{ display: 'flex', flexDirection: 'column', gap: '20px' }}>
                  <div className="setting-row">
                    <div>
                      <div className="setting-label">Slack Webhook URL</div>
                      <div className="setting-desc">Incoming webhook for your alerts channel (optional)</div>
                    </div>
                    <input
                      className="text-input"
                      type="url"
                      placeholder="https://hooks.slack.com/services/T0…"
                      value={settings.slack_webhook_url}
                      onChange={e => updateSetting('slack_webhook_url', e.target.value)}
                    />
                  </div>
                  <div className="setting-row">
                    <div>
                      <div className="setting-label">Minimum Notification Severity</div>
                      <div className="setting-desc">Only send Slack notifications at or above this level</div>
                    </div>
                    <select
                      className="select-input"
                      value={settings.min_notify_severity}
                      onChange={e => updateSetting('min_notify_severity', e.target.value)}
                    >
                      <option value="error">Error only</option>
                      <option value="warn">Warning + Error</option>
                      <option value="info">All</option>
                    </select>
                  </div>
                </div>
              </div>

              {/* Capture info */}
              <div className="card">
                <div className="card-header"><div className="card-title">Capture Configuration</div></div>
                <div className="card-body">
                  <div style={{ fontSize: '13px', color: 'var(--text-muted)', lineHeight: 1.7 }}>
                    Capture settings are configured via CLI flags when starting the probe:<br />
                    <code style={{ background: 'var(--surface3)', padding: '8px 12px', borderRadius: '4px', display: 'block', marginTop: '8px', fontFamily: 'var(--mono)', fontSize: '12px', color: 'var(--text)' }}>
                      sudo ./sniffit-probe -iface lo -port 5672 -port 15672
                    </code>
                    <br />
                    Override the control plane URL via environment:<br />
                    <code style={{ background: 'var(--surface3)', padding: '8px 12px', borderRadius: '4px', display: 'block', marginTop: '8px', fontFamily: 'var(--mono)', fontSize: '12px', color: 'var(--text)' }}>
                      CONTROL_PLANE_URL=http://localhost:8080 sudo ./sniffit-probe …
                    </code>
                  </div>
                </div>
              </div>

            </div>
          </div>
        )}
      </div>

      
        {showLogin && (
          <div className="drawer-overlay" style={{zIndex: 9999}}>
            <div className="card" style={{ margin: 'auto', marginTop: '20vh', width: '400px', padding: '24px', background: 'var(--surface)'}}>
              <h2 style={{marginTop: 0}}>Login Required</h2>
              <div style={{ marginBottom: '16px', color: 'var(--text-muted)' }}>Enter your API key to continue.</div>
              <input className="text-input" style={{ width: '100%', marginBottom: '16px', boxSizing: 'border-box' }} type="password" value={apiKeyInput} onChange={e => setApiKeyInput(e.target.value)} placeholder="API Key" />
              <div style={{ display: 'flex', gap: '12px', justifyContent: 'flex-end' }}>
                <button className="btn btn-primary" onClick={async () => {
                  const res = await fetch('/auth/login', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ api_key: apiKeyInput })
                  });
                  if (res.ok) {
                    const data = await res.json();
                    setAuthToken(data.token);
                    setIsAdmin(data.is_admin === 'true');
                    setTenantId(data.tenant_id);
                    localStorage.setItem('sniffit_token', data.token);
                    localStorage.setItem('sniffit_is_admin', data.is_admin);
                    localStorage.setItem('sniffit_tenant_id', data.tenant_id);
                    setShowLogin(false);
                    window.location.reload();
                  } else {
                    alert('Invalid API Key');
                  }
                }}>Login</button>
              </div>
            </div>
          </div>
        )}

        {/* ADMIN */}
        {activePage === 'admin' && isAdmin && (
          <div className="page">
            <div className="page-header">
              <div>
                <h1 style={{ fontSize: '20px', fontWeight: 600, color: '#fff', margin: 0 }}>Admin Panel</h1>
                <p style={{ margin: 0, fontSize: '13px', color: 'var(--text-muted)', marginTop: '3px' }}>Manage Tenants and API Keys.</p>
              </div>
            </div>
            <div style={{color: 'var(--text-dim)', fontSize: '13px', lineHeight: 1.6}}>
               Management operations are temporarily restricted to the CLI for full programmatic capabilities:
               <br/><br/>
               <code>
                  # List keys<br/>
                  curl -H "Authorization: Bearer $KEY" http://localhost:8080/api/keys<br/>
                  <br/>
                  # Create tenant<br/>
                  curl -X POST -d '{`{"id":"acme","name":"Acme Corp"}`}' http://localhost:8080/api/tenants<br/>
               </code>
            </div>
          </div>
        )}

{/* -- Alert Detail Drawer --------------------------------------─ */}
      {selectedAlert && (
        <div className="drawer-overlay" onClick={e => { if (e.target === e.currentTarget) setSelectedAlert(null); }}>
          <div className="drawer">
            <div className="drawer-header">
              <div style={{ flex: 1 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: '10px', marginBottom: '4px' }}>
                  <SevBadge sev={selectedAlert.severity} />
                  <span style={{ fontFamily: 'var(--mono)', fontSize: '12px', color: 'var(--text-dim)' }}>Rule {selectedAlert.rule_id}</span>
                  {selectedAlert.method_name && (
                    <span style={{ fontFamily: 'var(--mono)', fontSize: '12px', color: 'var(--accent)', background: 'var(--accent-light)', padding: '2px 6px', borderRadius: '3px' }}>
                      {selectedAlert.method_name}
                    </span>
                  )}
                </div>
                <div style={{ fontSize: '12px', color: 'var(--text-muted)' }}>
                  {new Date(selectedAlert.timestamp).toLocaleString()}
                </div>
              </div>
              <button className="drawer-close" onClick={() => setSelectedAlert(null)}>X</button>
            </div>
            <div className="drawer-body">

              {/* Diagnosis */}
              <section>
                <div className="drawer-section-title" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                  <span>What happened</span>
                  {!selectedAlert.ai_diagnosis && !aiLoading && (
                    <button className="btn btn-ghost" style={{ fontSize: '11px', padding: '3px 8px' }} onClick={handleAskAI}>
                      Ask AI
                    </button>
                  )}
                  {aiLoading && <span style={{ fontSize: '11px', color: 'var(--text-dim)' }}>Analyzing…</span>}
                </div>
                <div style={{
                  padding: '12px 14px', borderRadius: '6px', fontSize: '13px', lineHeight: 1.7, color: 'var(--text)',
                  background: 'var(--surface2)', border: `1px solid var(--border)`,
                  borderLeft: `3px solid ${SEV_COLOR[selectedAlert.severity]}`,
                }}>
                  {selectedAlert.layman}
                </div>
                {selectedAlert.ai_diagnosis && (
                  <div style={{
                    marginTop: '10px', padding: '12px 14px', borderRadius: '6px', fontSize: '13px', lineHeight: 1.7,
                    background: 'var(--surface2)', border: '1px solid var(--border)',
                  }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: '6px', marginBottom: '8px', color: 'var(--accent)', fontWeight: 600, fontSize: '12px' }}>
                      <Activity size={14} /> AI Analysis
                    </div>
                    <div style={{ color: 'var(--text)', whiteSpace: 'pre-wrap' }}>{selectedAlert.ai_diagnosis}</div>
                  </div>
                )}
              </section>

              {/* Context */}
              <section>
                <div className="drawer-section-title">Context</div>
                <div className="kv-table">
                  <div className="kv-row"><span>Rule</span><span className="mono">{selectedAlert.rule_name}</span></div>
                  <div className="kv-row"><span>Method</span><span className="mono">{selectedAlert.method_name || '---'}</span></div>
                  {selectedAlert.entity && <div className="kv-row"><span>Entity</span><span className="mono">{selectedAlert.entity}</span></div>}
                  <div className="kv-row"><span>Source</span><span className="mono">{getSource(selectedAlert)}</span></div>
                  <div className="kv-row"><span>Broker</span><span className="mono">{getBroker(selectedAlert)}</span></div>
                  <div className="kv-row"><span>Time</span><span className="mono">{new Date(selectedAlert.timestamp).toLocaleString()}</span></div>
                </div>
              </section>

              {/* Raw JSON */}
              <section>
                <div className="drawer-section-title">Raw Payload</div>
                <pre style={{
                  background: 'var(--surface2)', border: '1px solid var(--border)', borderRadius: '6px',
                  padding: '12px', fontSize: '11px', lineHeight: 1.6, overflow: 'auto', color: 'var(--text)',
                  fontFamily: 'var(--mono)', maxHeight: '240px', whiteSpace: 'pre-wrap',
                }}>
                  {JSON.stringify(selectedAlert, null, 2)}
                </pre>
              </section>

            </div>
          </div>
        </div>
      )}
    </>
  );
}
