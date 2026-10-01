import React, { useState, useEffect } from 'react';
import {
  Zap,
  Send,
  Flame,
  CheckCircle2,
  XCircle,
  RefreshCw,
  Database,
  Sliders,
  Clock,
  History,
  X,
  Trash2,
  ChevronRight,
  ChevronDown
} from 'lucide-react';

export default function App() {
  const [apiKey, setApiKey] = useState('client-alpha');
  const [message, setMessage] = useState('Hello FluxGate!');
  const [isLoading, setIsLoading] = useState(false);
  const [isBursting, setIsBursting] = useState(false);
  
  // Rate Limit Policy Settings (No. of Requests & Timestamp)
  const [policyLimit, setPolicyLimit] = useState(10);
  const [policyWindowSec, setPolicyWindowSec] = useState(60);
  const [policyFeedback, setPolicyFeedback] = useState(null);
  const [isSavingPolicy, setIsSavingPolicy] = useState(false);
  const [burstCount, setBurstCount] = useState(10);

  // Response, Quota & System State
  const [response, setResponse] = useState(null);
  const [quota, setQuota] = useState({ limit: 10, remaining: 10, reset: 0, retryAfter: 0 });
  const [history, setHistory] = useState([]);
  const [health, setHealth] = useState({ gateway: 'UP', redis: 'READY' });

  // Sidebar State
  const [isSidebarOpen, setIsSidebarOpen] = useState(false);
  const [expandedId, setExpandedId] = useState(null);
  const [statusFilter, setStatusFilter] = useState('all');

  // Compute gateway base URL (port 8080 if running separate from gateway)
  const getGatewayUrl = () => {
    if (window.location.port === '8080') return '';
    return `${window.location.protocol}//${window.location.hostname}:8080`;
  };

  // Health check
  const checkHealth = async () => {
    const base = getGatewayUrl();
    try {
      const hRes = await fetch(`${base}/health`).then(r => r.json()).catch(() => ({ status: 'DOWN' }));
      const rRes = await fetch(`${base}/ready`).then(r => r.json()).catch(() => ({ status: 'DOWN' }));
      setHealth({ gateway: hRes.status || 'DOWN', redis: rRes.status || 'DOWN' });
    } catch {
      setHealth({ gateway: 'DOWN', redis: 'DOWN' });
    }
  };

  useEffect(() => {
    checkHealth();
    const interval = setInterval(checkHealth, 5000);
    return () => clearInterval(interval);
  }, []);

  // Fetch current policy for selected API key from Redis
  const fetchCurrentPolicy = async (key = apiKey) => {
    const targetKey = key.trim() || 'default';
    const base = getGatewayUrl();
    try {
      const res = await fetch(`${base}/api/v1/config?key=${encodeURIComponent(targetKey)}`);
      if (res.ok) {
        const data = await res.json();
        setPolicyLimit(data.limit);
        setPolicyWindowSec(Math.round(data.window_ms / 1000));
        setQuota(prev => ({ ...prev, limit: data.limit }));
      }
    } catch {
      // ignore
    }
  };

  useEffect(() => {
    fetchCurrentPolicy(apiKey);
  }, [apiKey]);

  // Save updated policy (Limit & Window) to Redis
  const handleSavePolicy = async (e) => {
    if (e) e.preventDefault();
    setIsSavingPolicy(true);
    setPolicyFeedback(null);
    const targetKey = apiKey.trim() || 'default';
    const base = getGatewayUrl();

    try {
      const res = await fetch(`${base}/api/v1/config`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          key: targetKey,
          limit: Number(policyLimit),
          window_ms: Number(policyWindowSec) * 1000
        })
      });
      const data = await res.json();
      if (res.ok) {
        setPolicyFeedback({
          type: 'success',
          text: `Applied to Redis: ${policyLimit} requests per ${policyWindowSec}s for '${targetKey}'`
        });
        setQuota(prev => ({ ...prev, limit: Number(policyLimit) }));
      } else {
        setPolicyFeedback({ type: 'error', text: data.error || 'Failed to update rule' });
      }
    } catch (err) {
      setPolicyFeedback({ type: 'error', text: err.message });
    } finally {
      setIsSavingPolicy(false);
    }
  };

  // Send a single request
  const handleSend = async () => {
    if (isLoading) return;
    setIsLoading(true);
    const start = performance.now();

    const headers = { 'Content-Type': 'application/json' };
    if (apiKey.trim()) headers['X-API-Key'] = apiKey.trim();

    try {
      const res = await fetch(`${getGatewayUrl()}/api/v1/echo`, {
        method: 'POST',
        headers,
        body: JSON.stringify({ message })
      });

      const elapsed = Math.round(performance.now() - start);
      const limitH = res.headers.get('X-RateLimit-Limit');
      const remainingH = res.headers.get('X-RateLimit-Remaining');
      const resetH = res.headers.get('X-RateLimit-Reset');
      const retryAfterH = res.headers.get('Retry-After');

      let body = {};
      try {
        body = await res.json();
      } catch {
        body = { error: 'Failed to parse JSON response' };
      }

      if (limitH) {
        setQuota({
          limit: parseInt(limitH, 10),
          remaining: remainingH !== null ? parseInt(remainingH, 10) : 0,
          reset: resetH ? parseInt(resetH, 10) : 0,
          retryAfter: retryAfterH ? parseInt(retryAfterH, 10) : 0
        });
      }

      const item = {
        id: Math.random().toString(36).substring(7),
        time: new Date().toLocaleTimeString(),
        apiKey: apiKey.trim() || '(No Key)',
        payload: { message },
        status: res.status,
        statusText: res.statusText,
        elapsed,
        remaining: remainingH ?? '-',
        limit: limitH ?? '-',
        retryAfter: retryAfterH,
        body
      };

      setResponse(item);
      setHistory(prev => [item, ...prev.slice(0, 99)]);
    } catch (err) {
      setResponse({
        status: 0,
        statusText: 'Connection Failed',
        elapsed: 0,
        body: { error: err.message }
      });
    } finally {
      setIsLoading(false);
    }
  };

  // Test rate limiting by sending a burst of requests
  const handleBurstTest = async () => {
    if (isBursting) return;
    setIsBursting(true);
    const count = Number(burstCount) || 10;

    for (let i = 1; i <= count; i++) {
      const start = performance.now();
      const headers = { 'Content-Type': 'application/json' };
      if (apiKey.trim()) headers['X-API-Key'] = apiKey.trim();

      try {
        const res = await fetch(`${getGatewayUrl()}/api/v1/echo`, {
          method: 'POST',
          headers,
          body: JSON.stringify({ message: `Burst #${i}` })
        });
        const elapsed = Math.round(performance.now() - start);
        const remH = res.headers.get('X-RateLimit-Remaining');
        const limH = res.headers.get('X-RateLimit-Limit');
        const retryAfterH = res.headers.get('Retry-After');

        let body = {};
        try { body = await res.json(); } catch {}

        if (limH) {
          setQuota({
            limit: parseInt(limH, 10),
            remaining: remH !== null ? parseInt(remH, 10) : 0,
            reset: 0,
            retryAfter: retryAfterH ? parseInt(retryAfterH, 10) : 0
          });
        }

        const item = {
          id: Math.random().toString(36).substring(7),
          time: new Date().toLocaleTimeString(),
          apiKey: apiKey.trim() || '(No Key)',
          payload: { message: `Burst #${i}` },
          status: res.status,
          statusText: res.statusText,
          elapsed,
          remaining: remH ?? '-',
          limit: limH ?? '-',
          retryAfter: retryAfterH,
          body
        };

        setResponse(item);
        setHistory(prev => [item, ...prev.slice(0, 99)]);
      } catch (err) {
        // network issue
      }
      await new Promise(r => setTimeout(r, 40));
    }

    setIsBursting(false);
  };

  const filteredHistory = history.filter(item => {
    if (statusFilter === '200') return item.status === 200;
    if (statusFilter === '429') return item.status === 429;
    if (statusFilter === 'error') return item.status !== 200 && item.status !== 429;
    return true;
  });

  const progressPercent = quota.limit > 0 ? Math.max(0, Math.min(100, Math.round((quota.remaining / quota.limit) * 100))) : 0;

  return (
    <div className="min-h-screen bg-[#0b0f19] text-slate-100 flex flex-col justify-between selection:bg-blue-600 selection:text-white relative">
      {/* Top Header */}
      <header className="border-b border-white/10 bg-[#0f172a]/60 px-6 py-4 sticky top-0 z-30 backdrop-blur-md">
        <div className="max-w-5xl mx-auto flex items-center justify-between">
          <div className="flex items-center gap-3">
            <div className="h-9 w-9 rounded-xl bg-blue-600 flex items-center justify-center shadow-lg shadow-blue-500/20">
              <Zap className="h-5 w-5 text-white" />
            </div>
            <div>
              <h1 className="text-lg font-bold tracking-tight text-white flex items-center gap-2">
                FluxGate
                <span className="text-[11px] font-mono font-medium px-2 py-0.5 rounded bg-blue-500/10 text-blue-400 border border-blue-500/20">
                  Rate Limiter
                </span>
              </h1>
            </div>
          </div>

          {/* Right actions: History button & System status badges */}
          <div className="flex items-center gap-3 text-xs font-mono">
            {/* Sidebar Toggle Button */}
            <button
              onClick={() => setIsSidebarOpen(true)}
              className="flex items-center gap-1.5 px-3 py-1.5 rounded-xl bg-slate-800 hover:bg-slate-700 text-slate-200 border border-white/10 transition shadow-sm"
              title="Open previous requests sidebar"
            >
              <History className="h-3.5 w-3.5 text-blue-400" />
              <span className="font-sans font-medium hidden sm:inline">History</span>
              <span className="px-1.5 py-0.2 rounded-full bg-blue-600 text-[10px] text-white font-mono font-bold">
                {history.length}
              </span>
            </button>

            <div className="flex items-center gap-1.5 px-2.5 py-1 rounded-full bg-slate-900 border border-white/10">
              <span className={`h-2 w-2 rounded-full ${health.gateway === 'UP' ? 'bg-emerald-400 animate-pulse' : 'bg-rose-500'}`} />
              <span className="text-slate-400">Gateway:</span>
              <span className={health.gateway === 'UP' ? 'text-emerald-400 font-semibold' : 'text-rose-400'}>{health.gateway}</span>
            </div>

            <div className="flex items-center gap-1.5 px-2.5 py-1 rounded-full bg-slate-900 border border-white/10">
              <Database className="h-3 w-3 text-cyan-400" />
              <span className="text-slate-400">Redis:</span>
              <span className={health.redis === 'READY' ? 'text-cyan-400 font-semibold' : 'text-amber-400'}>{health.redis}</span>
            </div>
          </div>
        </div>
      </header>

      {/* Main Content (2-Column Dashboard) */}
      <main className="max-w-5xl w-full mx-auto px-6 py-8 flex-1 space-y-6">
        <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
          
          {/* Left Column: API Request + Policy Settings */}
          <div className="space-y-6">
            
            {/* Card 1: Send API Request */}
            <div className="bg-slate-900/60 border border-white/10 rounded-2xl p-6 shadow-xl space-y-5">
              <h2 className="text-sm font-semibold uppercase tracking-wider text-slate-400 flex items-center gap-2">
                <Send className="h-4 w-4 text-blue-400" /> Send API Request
              </h2>

              {/* API Key Input */}
              <div>
                <label className="block text-xs text-slate-400 mb-1.5">Client API Key (X-API-Key)</label>
                <input
                  type="text"
                  value={apiKey}
                  onChange={(e) => setApiKey(e.target.value)}
                  placeholder="e.g. client-alpha (or leave empty to test 401)"
                  className="w-full px-3.5 py-2 rounded-xl bg-slate-950 border border-white/10 text-white font-mono text-sm focus:outline-none focus:border-blue-500 transition"
                />
                <div className="flex gap-2 mt-2">
                  <button
                    type="button"
                    onClick={() => setApiKey('client-alpha')}
                    className="text-[11px] px-2 py-0.5 rounded bg-slate-800 hover:bg-slate-700 text-slate-300 transition"
                  >
                    client-alpha
                  </button>
                  <button
                    type="button"
                    onClick={() => setApiKey('client-vip')}
                    className="text-[11px] px-2 py-0.5 rounded bg-slate-800 hover:bg-slate-700 text-slate-300 transition"
                  >
                    client-vip
                  </button>
                  <button
                    type="button"
                    onClick={() => setApiKey('')}
                    className="text-[11px] px-2 py-0.5 rounded bg-slate-800 hover:bg-slate-700 text-amber-300 transition"
                  >
                    No Key (401)
                  </button>
                </div>
              </div>

              {/* Message Body */}
              <div>
                <label className="block text-xs text-slate-400 mb-1.5">Payload Message</label>
                <input
                  type="text"
                  value={message}
                  onChange={(e) => setMessage(e.target.value)}
                  className="w-full px-3.5 py-2 rounded-xl bg-slate-950 border border-white/10 text-white font-mono text-sm focus:outline-none focus:border-blue-500 transition"
                />
              </div>

              {/* Action Buttons */}
              <div className="pt-1 space-y-2.5">
                <button
                  onClick={handleSend}
                  disabled={isLoading || isBursting}
                  className="w-full py-2.5 px-4 rounded-xl bg-blue-600 hover:bg-blue-500 text-white font-medium text-sm transition shadow-lg shadow-blue-500/20 disabled:opacity-50 flex items-center justify-center gap-2"
                >
                  {isLoading ? <RefreshCw className="h-4 w-4 animate-spin" /> : <Send className="h-4 w-4" />}
                  <span>Send Single Request</span>
                </button>

                {/* Adjustable Burst Trigger */}
                <div className="flex items-center gap-2">
                  <div className="flex-1">
                    <button
                      onClick={handleBurstTest}
                      disabled={isLoading || isBursting}
                      className="w-full py-2 px-3 rounded-xl bg-slate-800 hover:bg-slate-700 text-slate-200 text-xs font-medium transition border border-white/10 flex items-center justify-center gap-2 disabled:opacity-50"
                    >
                      {isBursting ? <RefreshCw className="h-3.5 w-3.5 animate-spin" /> : <Flame className="h-3.5 w-3.5 text-amber-400" />}
                      <span>Trigger Burst ({burstCount} Requests)</span>
                    </button>
                  </div>
                  <select
                    value={burstCount}
                    onChange={(e) => setBurstCount(Number(e.target.value))}
                    disabled={isLoading || isBursting}
                    className="px-2.5 py-2 rounded-xl bg-slate-800 border border-white/10 text-xs text-white font-mono focus:outline-none focus:border-blue-500"
                    title="Number of burst requests to fire"
                  >
                    <option value="5">5x</option>
                    <option value="10">10x</option>
                    <option value="15">15x</option>
                    <option value="20">20x</option>
                    <option value="30">30x</option>
                  </select>
                </div>
              </div>
            </div>

            {/* Card 2: Adjustable Policy Settings (No. of Requests & Timestamp/Window) */}
            <div className="bg-slate-900/60 border border-white/10 rounded-2xl p-6 shadow-xl space-y-4">
              <div className="flex items-center justify-between border-b border-white/10 pb-3">
                <h2 className="text-sm font-semibold uppercase tracking-wider text-slate-400 flex items-center gap-2">
                  <Sliders className="h-4 w-4 text-indigo-400" /> Rate Limit Policy (Redis)
                </h2>
                <span className="text-[11px] font-mono text-slate-400 px-2 py-0.5 rounded bg-slate-800">
                  Target: {apiKey.trim() || 'default'}
                </span>
              </div>

              <div className="grid grid-cols-2 gap-4">
                {/* Adjust Limit */}
                <div>
                  <label className="block text-xs text-slate-400 mb-1">
                    No. of Requests (Limit)
                  </label>
                  <input
                    type="number"
                    min="1"
                    max="1000"
                    value={policyLimit}
                    onChange={(e) => setPolicyLimit(Number(e.target.value))}
                    className="w-full px-3 py-2 rounded-xl bg-slate-950 border border-white/10 text-white font-mono text-sm focus:outline-none focus:border-indigo-500"
                  />
                  <div className="flex gap-1.5 mt-1.5">
                    {[5, 10, 20, 50].map(val => (
                      <button
                        key={val}
                        type="button"
                        onClick={() => setPolicyLimit(val)}
                        className={`text-[10px] px-2 py-0.5 rounded font-mono transition ${
                          policyLimit === val ? 'bg-indigo-600 text-white' : 'bg-slate-800 text-slate-400 hover:text-white'
                        }`}
                      >
                        {val}
                      </button>
                    ))}
                  </div>
                </div>

                {/* Adjust Window (Timestamp) */}
                <div>
                  <label className="block text-xs text-slate-400 mb-1 flex items-center gap-1">
                    <Clock className="h-3 w-3 text-cyan-400" /> Time Window (Seconds)
                  </label>
                  <input
                    type="number"
                    min="1"
                    max="3600"
                    value={policyWindowSec}
                    onChange={(e) => setPolicyWindowSec(Number(e.target.value))}
                    className="w-full px-3 py-2 rounded-xl bg-slate-950 border border-white/10 text-white font-mono text-sm focus:outline-none focus:border-indigo-500"
                  />
                  <div className="flex gap-1.5 mt-1.5">
                    {[10, 30, 60, 120].map(val => (
                      <button
                        key={val}
                        type="button"
                        onClick={() => setPolicyWindowSec(val)}
                        className={`text-[10px] px-2 py-0.5 rounded font-mono transition ${
                          policyWindowSec === val ? 'bg-cyan-600 text-white' : 'bg-slate-800 text-slate-400 hover:text-white'
                        }`}
                      >
                        {val}s
                      </button>
                    ))}
                  </div>
                </div>
              </div>

              {policyFeedback && (
                <div className={`p-2.5 rounded-xl text-xs font-mono ${
                  policyFeedback.type === 'success'
                    ? 'bg-emerald-500/10 text-emerald-400 border border-emerald-500/20'
                    : 'bg-rose-500/10 text-rose-400 border border-rose-500/20'
                }`}>
                  {policyFeedback.text}
                </div>
              )}

              <button
                onClick={handleSavePolicy}
                disabled={isSavingPolicy}
                className="w-full py-2 px-4 rounded-xl bg-indigo-600 hover:bg-indigo-500 text-white font-medium text-xs transition shadow-lg shadow-indigo-500/20 disabled:opacity-50 flex items-center justify-center gap-2"
              >
                {isSavingPolicy ? <RefreshCw className="h-3.5 w-3.5 animate-spin" /> : <Sliders className="h-3.5 w-3.5" />}
                <span>Apply Policy to Redis</span>
              </button>
            </div>
          </div>

          {/* Right Column: Rate Limit Status & Live Response */}
          <div className="space-y-6">
            
            {/* Quota Indicator Card */}
            <div className="bg-slate-900/60 border border-white/10 rounded-2xl p-6 shadow-xl space-y-3">
              <div className="flex justify-between items-center">
                <span className="text-xs uppercase tracking-wider text-slate-400 font-semibold">
                  Sliding Window Quota
                </span>
                <span className="font-mono text-xs text-slate-300">
                  {quota.remaining} / {quota.limit} remaining
                </span>
              </div>

              {/* Progress bar */}
              <div className="w-full h-3 bg-slate-950 rounded-full overflow-hidden border border-white/5">
                <div
                  className={`h-full transition-all duration-300 ${
                    progressPercent > 40 ? 'bg-blue-500' : progressPercent > 0 ? 'bg-amber-400' : 'bg-rose-500'
                  }`}
                  style={{ width: `${progressPercent}%` }}
                />
              </div>

              {/* Status explanation */}
              <div className="flex justify-between text-[11px] text-slate-400 font-mono">
                <span>Active Window: {policyWindowSec}s</span>
                <span>Reset: {quota.retryAfter > 0 ? `Penalty (${quota.retryAfter}s)` : 'Ready'}</span>
              </div>
            </div>

            {/* Last Response Card */}
            <div className="bg-slate-900/60 border border-white/10 rounded-2xl p-6 shadow-xl space-y-3">
              <div className="flex justify-between items-center">
                <span className="text-xs uppercase tracking-wider text-slate-400 font-semibold">
                  Latest Response
                </span>
                {response && (
                  <span className={`text-xs px-2 py-0.5 rounded font-mono font-semibold flex items-center gap-1 ${
                    response.status === 200
                      ? 'bg-emerald-500/10 text-emerald-400 border border-emerald-500/20'
                      : response.status === 429
                      ? 'bg-amber-500/10 text-amber-400 border border-amber-500/20'
                      : 'bg-rose-500/10 text-rose-400 border border-rose-500/20'
                  }`}>
                    {response.status === 200 ? <CheckCircle2 className="h-3 w-3" /> : <XCircle className="h-3 w-3" />}
                    {response.status} {response.statusText} ({response.elapsed}ms)
                  </span>
                )}
              </div>

              {/* Formatted response body */}
              <pre className="p-3.5 rounded-xl bg-slate-950 border border-white/10 font-mono text-xs text-slate-200 overflow-x-auto max-h-56 leading-relaxed">
                {response ? JSON.stringify(response.body, null, 2) : '// Click "Send Single Request" to test'}
              </pre>
            </div>
          </div>
        </div>

        {/* Quick Recent Requests Banner */}
        {history.length > 0 && (
          <div className="bg-slate-900/60 border border-white/10 rounded-2xl p-4 shadow-xl flex items-center justify-between">
            <div className="flex items-center gap-3 text-xs">
              <span className="text-slate-400 uppercase tracking-wider font-semibold">Previous Requests:</span>
              <span className="font-mono text-slate-200">{history.length} logged in session</span>
            </div>
            <button
              onClick={() => setIsSidebarOpen(true)}
              className="flex items-center gap-1.5 px-3 py-1.5 rounded-xl bg-blue-600/20 hover:bg-blue-600/30 text-blue-400 text-xs font-medium border border-blue-500/30 transition"
            >
              <span>View In Sidebar Drawer</span>
              <ChevronRight className="h-3.5 w-3.5" />
            </button>
          </div>
        )}
      </main>

      {/* Clean Footer */}
      <footer className="border-t border-white/10 bg-[#0f172a]/60 py-4 px-6 text-center text-xs text-slate-400">
        <div className="max-w-5xl mx-auto flex flex-col sm:flex-row items-center justify-between gap-2">
          <span>FluxGate — Academic Engineering Project</span>
          <span className="font-mono text-slate-300">
            Raghav Gupta • Rishabh Srivastava • Rishabh Singh • Prince Keshari
          </span>
        </div>
      </footer>

      {/* Slide-out Sidebar Drawer for Previous Requests */}
      {/* Backdrop */}
      <div
        onClick={() => setIsSidebarOpen(false)}
        className={`fixed inset-0 bg-black/60 backdrop-blur-xs z-40 transition-opacity duration-300 ${
          isSidebarOpen ? 'opacity-100 pointer-events-auto' : 'opacity-0 pointer-events-none'
        }`}
      />

      {/* Sidebar Panel */}
      <aside
        className={`fixed top-0 right-0 h-full w-full sm:w-[440px] bg-[#0c1322] border-l border-white/10 z-50 flex flex-col shadow-2xl transition-transform duration-300 ease-in-out ${
          isSidebarOpen ? 'translate-x-0' : 'translate-x-full'
        }`}
      >
        {/* Sidebar Header */}
        <div className="p-4 border-b border-white/10 flex items-center justify-between bg-slate-900/60">
          <div className="flex items-center gap-2">
            <History className="h-4 w-4 text-blue-400" />
            <h3 className="font-semibold text-white text-sm">Previous Requests</h3>
            <span className="px-2 py-0.5 rounded-full bg-blue-500/10 text-blue-400 text-xs font-mono font-medium border border-blue-500/20">
              {history.length}
            </span>
          </div>

          <div className="flex items-center gap-2">
            {history.length > 0 && (
              <button
                onClick={() => setHistory([])}
                className="p-1.5 rounded-lg text-slate-400 hover:text-rose-400 hover:bg-slate-800 transition"
                title="Clear all history"
              >
                <Trash2 className="h-4 w-4" />
              </button>
            )}
            <button
              onClick={() => setIsSidebarOpen(false)}
              className="p-1.5 rounded-lg text-slate-400 hover:text-white hover:bg-slate-800 transition"
              title="Close sidebar"
            >
              <X className="h-4 w-4" />
            </button>
          </div>
        </div>

        {/* Filter Pills */}
        <div className="px-4 py-2.5 border-b border-white/5 flex gap-2 text-xs font-mono bg-slate-950/40">
          {[
            { id: 'all', label: `All (${history.length})` },
            { id: '200', label: `200 OK (${history.filter(h => h.status === 200).length})` },
            { id: '429', label: `429 (${history.filter(h => h.status === 429).length})` },
          ].map(f => (
            <button
              key={f.id}
              onClick={() => setStatusFilter(f.id)}
              className={`px-2.5 py-1 rounded-lg text-[11px] transition ${
                statusFilter === f.id
                  ? 'bg-blue-600 text-white font-semibold'
                  : 'bg-slate-800 text-slate-400 hover:text-white'
              }`}
            >
              {f.label}
            </button>
          ))}
        </div>

        {/* Scrollable Slidebar / Request List Container */}
        <div className="flex-1 overflow-y-auto p-4 space-y-2.5 custom-slidebar">
          {filteredHistory.map(item => {
            const isExpanded = expandedId === item.id;
            return (
              <div
                key={item.id}
                className="rounded-xl border border-white/5 bg-slate-900/60 p-3 text-xs font-mono transition hover:border-white/20"
              >
                {/* Item Summary Bar */}
                <div
                  onClick={() => setExpandedId(isExpanded ? null : item.id)}
                  className="flex items-center justify-between cursor-pointer select-none"
                >
                  <div className="flex items-center gap-2">
                    <span className={`px-2 py-0.5 rounded text-[11px] font-bold ${
                      item.status === 200
                        ? 'bg-emerald-500/10 text-emerald-400 border border-emerald-500/20'
                        : item.status === 429
                        ? 'bg-amber-500/10 text-amber-400 border border-amber-500/20'
                        : 'bg-rose-500/10 text-rose-400 border border-rose-500/20'
                    }`}>
                      {item.status}
                    </span>
                    <span className="text-slate-300 font-medium">{item.apiKey}</span>
                  </div>

                  <div className="flex items-center gap-2 text-slate-400">
                    <span className="text-[11px] text-cyan-400">{item.elapsed}ms</span>
                    <span className="text-[11px] text-slate-500">{item.time}</span>
                    {isExpanded ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
                  </div>
                </div>

                {/* Sub-details */}
                <div className="flex justify-between items-center mt-2 pt-2 border-t border-white/5 text-[11px] text-slate-400">
                  <span>Quota Left: {item.remaining} / {item.limit}</span>
                  {item.retryAfter && (
                    <span className="text-rose-400">Penalty: {item.retryAfter}s</span>
                  )}
                </div>

                {/* Expanded Details */}
                {isExpanded && (
                  <div className="mt-2.5 pt-2 border-t border-white/5 space-y-2">
                    <div>
                      <div className="text-[10px] text-slate-500 uppercase tracking-wider mb-1">Payload Sent:</div>
                      <pre className="p-2 rounded-lg bg-slate-950 text-[11px] text-slate-300 overflow-x-auto">
                        {JSON.stringify(item.payload, null, 2)}
                      </pre>
                    </div>

                    <div>
                      <div className="text-[10px] text-slate-500 uppercase tracking-wider mb-1">Gateway Response:</div>
                      <pre className="p-2 rounded-lg bg-slate-950 text-[11px] text-slate-300 overflow-x-auto max-h-40">
                        {JSON.stringify(item.body, null, 2)}
                      </pre>
                    </div>
                  </div>
                )}
              </div>
            );
          })}

          {filteredHistory.length === 0 && (
            <div className="text-center py-16 text-slate-500 text-xs">
              <History className="h-8 w-8 mx-auto mb-2 opacity-30" />
              <p>No requests recorded yet.</p>
              <p className="text-[11px] text-slate-600 mt-1">Send a single request or burst test to view history.</p>
            </div>
          )}
        </div>
      </aside>
    </div>
  );
}
