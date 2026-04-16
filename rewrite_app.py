import re

with open('ui/src/App.tsx', 'r') as f:
    content = f.read()

# 1. State variables
state_vars = """
  const [showLogin, setShowLogin] = useState(false);
  const [authToken, setAuthToken] = useState(localStorage.getItem('sniffit_token') || '');
  const [apiKeyInput, setApiKeyInput] = useState('');
  const [isAdmin, setIsAdmin] = useState(localStorage.getItem('sniffit_is_admin') === 'true');
  const [tenantId, setTenantId] = useState(localStorage.getItem('sniffit_tenant_id') || '');

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

"""
content = re.sub(
    r'(const \[topoLoading, setTopoLoading\] = useState\(false\);)',
    r'\1\n' + state_vars,
    content
)

# 2. Replace 'fetch(' with 'apiFetch(' except for the clear alerts / clear history inside useEffect
# Actually just dynamically replace 'fetch(\'/api/' to 'apiFetch(\'/api/'
content = content.replace("fetch('/api/", "apiFetch('/api/")

# 3. SSE event source
sse_rewrite = r"""
    const tokenParam = authToken ? `?token=${authToken}` : '';
    const evtSource = new EventSource('/api/events' + tokenParam);
"""
content = re.sub(
    r'const evtSource = new EventSource\(\'/api/events\'\);',
    sse_rewrite.strip(),
    content
)

# 4. Modals and Tabs
# Add 'admin' to tabs list
content = content.replace("['dashboard', 'events', 'rules', 'topology', 'ai', 'settings']", "['dashboard', 'events', 'rules', 'topology', 'ai', 'settings', ...(isAdmin ? ['admin'] : [])]")

# 5. Insert login modal and admin page just above exactly this string: "{/* -- Alert Detail Drawer"
login_modal = """
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
                  curl -X POST -d '{{"id":"acme","name":"Acme Corp"}}' http://localhost:8080/api/tenants<br/>
               </code>
            </div>
          </div>
        )}

"""

content = content.replace("{/* -- Alert Detail Drawer", login_modal + "{/* -- Alert Detail Drawer")

with open('ui/src/App.tsx', 'w') as f:
    f.write(content)

