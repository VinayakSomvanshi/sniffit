/**
 * @vitest-environment happy-dom
 * @bun-test environment
 */
/// <reference lib="dom" />
import { render, screen, fireEvent, act, within } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import App from './App';

const MOCK_ALERTS = [
  {
    severity: "error",
    rule_id: "1.1",
    rule_name: "Authentication Refused",
    layman: "Authentication failed",
    timestamp: new Date().toISOString(),
    amqp_frame_raw: "...",
    pod_cpu_pct: 10,
    pod_mem_mb: 100
  },
  {
    severity: "warn",
    rule_id: "4.3",
    rule_name: "Message Rejected",
    layman: "Consumer rejected message",
    timestamp: new Date(Date.now() - 60000).toISOString(),
    amqp_frame_raw: "...",
    pod_cpu_pct: 5,
    pod_mem_mb: 50
  }
];

// Mock Lucide icons
vi.mock('lucide-react', () => {
  const icons = [
    'Activity', 'CheckCircle2', 'TerminalSquare', 'Settings', 'RefreshCw', 'Trash2',
    'ChevronRight', 'Circle', 'Search', 'Download', 'Database', 'Wifi', 'WifiOff',
    'MessageSquare', 'Sparkles', 'Send', 'Info', 'RefreshCcw', 'Lock', 'Zap',
    'Box', 'Globe', 'Users', 'LogOut', 'Plus', 'ExternalLink', 'Menu', 'X',
    'ChevronDown', 'ChevronUp', 'Copy', 'Check', 'AlertTriangle', 'Network',
    'Bot', 'Clock', 'Shield', 'Cpu', 'Layers', 'XCircle',
  ];
  return icons.reduce((acc, name) => {
    acc[name] = () => <div data-testid={`icon-${name}`}>{name}</div>;
    return acc;
  }, {} as any);
});

describe('SniffIt APP', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    
    // Setup robust fetch mock for all tests
    global.fetch = vi.fn().mockImplementation((input: any, init?: any) => {
      const url = typeof input === 'string' ? input : input.url;
      const method = init?.method || 'GET';
      
      if (method === 'DELETE' && url.includes('/api/alerts')) return Promise.resolve({ ok: true });
      if (url.includes('/api/alerts/history')) return Promise.resolve({ ok: true, json: () => Promise.resolve([...MOCK_ALERTS]) });
      if (url.includes('/api/settings')) return Promise.resolve({ ok: true, json: () => Promise.resolve({ ai_trigger_level: 'error', slack_webhook_url: '', min_notify_severity: 'warn' }) });
      if (url.includes('/api/ai/health')) return Promise.resolve({ ok: true, json: () => Promise.resolve({ score: 85, status: 'healthy', summary: 'System looks good' }) });
      if (url.includes('/api/topology')) return Promise.resolve({ ok: true, json: () => Promise.resolve({ last_updated: new Date().toISOString(), queues: [], exchanges: [], consumers: [] }) });
      if (url.includes('/api/ai/stream')) return Promise.resolve({ ok: true, json: () => Promise.resolve({ top_issues: 'Issues', patterns: 'Patterns', root_cause: 'Root', recommendations: 'Recs' }) });
      if (url.includes('/api/analyze')) return Promise.resolve({ ok: true, json: () => Promise.resolve({ diagnosis: 'AI says check credentials' }) });
      
      return Promise.resolve({ ok: true, json: () => Promise.resolve({}) });
    }) as any;

    window.confirm = vi.fn(() => true);
  });

  it('renders and hydrates with initial data', async () => {
    render(<App />);
    expect(await screen.findByText(/SniffIt/i)).toBeInTheDocument();
    expect(await screen.findByText(/Authentication failed/i)).toBeInTheDocument();
    expect(screen.getByText(/Consumer rejected message/i)).toBeInTheDocument();
    expect(screen.getByText(/85\/100/i)).toBeInTheDocument();
  });

  it('handles real-time events via SSE', async () => {
    render(<App />);
    await screen.findByText(/SniffIt/i);

    const newAlert = {
      severity: "info",
      rule_id: "9.9",
      rule_name: "New Event",
      layman: "Live event update",
      timestamp: new Date().toISOString(),
      amqp_frame_raw: "..."
    };

    act(() => {
      if ((global as any).lastEventSource) {
        (global as any).lastEventSource.emit(newAlert);
      }
    });

    expect(await screen.findByText(/Live event update/i)).toBeInTheDocument();
  });

  it('navigates through application tabs', async () => {
    render(<App />);
    const nav = screen.getByRole('navigation');

    // Topology Tab
    const topoTab = within(nav).getByRole('button', { name: /topology/i });
    await act(async () => {
      fireEvent.click(topoTab);
    });
    expect(await screen.findByText(/Derived from captured AMQP traffic/i)).toBeInTheDocument();

    // AI Hub Tab
    const aiTab = within(nav).getByRole('button', { name: /ai hub/i });
    await act(async () => {
      fireEvent.click(aiTab);
    });
    expect(await screen.findByText(/AI Diagnostic Hub/i)).toBeInTheDocument();

    // Settings Tab
    const settingsTab = within(nav).getByRole('button', { name: /settings/i });
    await act(async () => {
      fireEvent.click(settingsTab);
    });
    expect(await screen.findByText(/Persisted to disk — survive restarts/i)).toBeInTheDocument();
  });

  it('filters events by severity', async () => {
    render(<App />);
    const nav = screen.getByRole('navigation');
    fireEvent.click(within(nav).getByRole('button', { name: /events/i }));

    await screen.findByText(/All/i);
    
    // Filter by Error
    const errorFilter = screen.getByRole('button', { name: /error/i });
    fireEvent.click(errorFilter);
    expect(await screen.findByText(/Authentication failed/i)).toBeInTheDocument();
    expect(screen.queryByText(/Consumer rejected message/i)).not.toBeInTheDocument();
  });

  it('performs global search', async () => {
    render(<App />);
    fireEvent.click(screen.getByRole('button', { name: /events/i }));

    const searchInput = await screen.findByPlaceholderText(/search events/i);
    fireEvent.change(searchInput, { target: { value: 'Authentication' } });
    
    expect(await screen.findByText(/Authentication failed/i)).toBeInTheDocument();
    expect(screen.queryByText(/Consumer rejected message/i)).not.toBeInTheDocument();
  });

  it('interacts with alert details and AI diagnosis', async () => {
    render(<App />);
    const alertRow = await screen.findByText(/Authentication failed/i);
    fireEvent.click(alertRow);

    expect(await screen.findByText(/What happened/i)).toBeInTheDocument();
    
    const askAiBtn = screen.getByRole('button', { name: /ask ai/i });
    
    await act(async () => {
      fireEvent.click(askAiBtn);
    });

    expect(await screen.findByText(/AI Analysis/i)).toBeInTheDocument();
    // Use findAllByText for AI diagnosis as it may appear in both diagnosis text and raw payload
    const matches = await screen.findAllByText(/AI says check credentials/i);
    expect(matches.length).toBeGreaterThan(0);
  });

  it('handles clearing history', async () => {
    render(<App />);
    // Ensure data loaded first
    await screen.findByText(/Authentication failed/i);

    const clearBtn = screen.getByTitle(/clear all history/i);
    
    await act(async () => {
      fireEvent.click(clearBtn);
    });

    // Wait for the UI to update to empty state
    expect(await screen.findByText(/No events captured yet/i)).toBeInTheDocument();
    expect(screen.queryByText(/Authentication failed/i)).not.toBeInTheDocument();
  });

  it('handles settings updates', async () => {
    render(<App />);
    fireEvent.click(screen.getByRole('button', { name: /settings/i }));
    
    // Wait for data
    const thresholdSelect = await screen.findByDisplayValue(/errors only/i);
    fireEvent.change(thresholdSelect, { target: { value: 'warn' } });
    
    const saveBtn = screen.getByRole('button', { name: /save settings/i });
    
    await act(async () => {
      fireEvent.click(saveBtn);
    });

    expect(await screen.findByText(/OK Saved/i, {}, { timeout: 3000 })).toBeInTheDocument();
  });
});
