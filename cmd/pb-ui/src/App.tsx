import { useEffect, useMemo, useState } from "react";

type Sandbox = {
  id: string;
  status: string;
  health_status?: string;
  driver?: string;
  namespace?: string;
  rpc_endpoint?: string;
  expires_at?: number;
  metadata?: Record<string, string>;
};

type EventRecord = {
  id?: number;
  timestamp?: string;
  type: string;
  sandbox_id?: string;
  method?: string;
  message?: string;
  stream?: string;
  data?: unknown;
};

const formatTimestamp = (value?: string) => {
  if (!value) return "--";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
};

const formatExpiry = (epoch?: number) => {
  if (!epoch) return "No TTL";
  return new Date(epoch * 1000).toLocaleString();
};

export function App() {
  const [sandboxes, setSandboxes] = useState<Sandbox[]>([]);
  const [events, setEvents] = useState<EventRecord[]>([]);
  const [selectedSandbox, setSelectedSandbox] = useState<string>("");
  const [connectionState, setConnectionState] = useState("connecting");
  const [showSettings, setShowSettings] = useState(false);
  const [apiKey, setApiKey] = useState(() => localStorage.getItem("PB_API_KEY") || "");

  const saveApiKey = (key: string) => {
    setApiKey(key);
    localStorage.setItem("PB_API_KEY", key);
  };

  useEffect(() => {
    void fetch("/api/v1/sandboxes")
      .then((response) => response.json())
      .then((payload) => {
        const items: Sandbox[] = payload.sandboxes ?? [];
        setSandboxes(items);
        if (!selectedSandbox && items[0]) {
          setSelectedSandbox(items[0].id);
        }
      })
      .catch(() => undefined);
  }, [selectedSandbox]);

  useEffect(() => {
    const source = new EventSource("/api/v1/events/stream");
    source.onopen = () => setConnectionState("live");
    source.onerror = () => setConnectionState("reconnecting");
    source.onmessage = () => undefined;

    const handler = (event: MessageEvent) => {
      const payload = JSON.parse(event.data) as EventRecord;
      setEvents((current) => [payload, ...current].slice(0, 200));
      if (payload.sandbox_id) {
        setSandboxes((current) => {
          const next = [...current];
          const index = next.findIndex((item) => item.id === payload.sandbox_id);
          if (index >= 0) {
            next[index] = {
              ...next[index],
              status: payload.type.startsWith("sandbox.") ? payload.type.replace("sandbox.", "") : next[index].status,
            };
            return next;
          }
          return current;
        });
      }
    };

    const eventTypes = [
      "rpc.started",
      "rpc.completed",
      "rpc.error",
      "sandbox.created",
      "sandbox.started",
      "sandbox.stopped",
      "sandbox.destroyed",
      "sandbox.reaped",
      "db.progress",
      "browser.progress",
    ];
    eventTypes.forEach((type) => source.addEventListener(type, handler as EventListener));

    return () => {
      eventTypes.forEach((type) => source.removeEventListener(type, handler as EventListener));
      source.close();
    };
  }, []);

  const selected = useMemo(
    () => sandboxes.find((item) => item.id === selectedSandbox) ?? sandboxes[0],
    [sandboxes, selectedSandbox],
  );
  const filteredEvents = useMemo(
    () => events.filter((item) => !selected || !item.sandbox_id || item.sandbox_id === selected.id),
    [events, selected],
  );

  return (
    <main className="inspector-shell">
      <section className="hero-panel">
        <div>
          <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
            <p className="eyebrow">PrimitiveBox Control Plane</p>
            <button className="settings-btn" onClick={() => setShowSettings(true)}>
              Settings
            </button>
          </div>
          <h1>Telemetry that reads like a flight recorder.</h1>
          <p className="hero-copy">
            Watch sandbox lifecycles, RPC edges, browser automation, and read-only database access flow through the
            same event spine.
          </p>
        </div>
        <div className="hero-metrics">
          <div className="metric-card">
            <span>Stream</span>
            <strong>{connectionState}</strong>
          </div>
          <div className="metric-card">
            <span>Sandboxes</span>
            <strong>{sandboxes.length}</strong>
          </div>
          <div className="metric-card">
            <span>Events Buffered</span>
            <strong>{events.length}</strong>
          </div>
        </div>
      </section>

      <section className="grid">
        <aside className="panel">
          <div className="panel-header">
            <h2>Sandbox Fleet</h2>
            <span>{sandboxes.length} active records</span>
          </div>
          <div className="sandbox-list">
            {sandboxes.map((sandbox) => (
              <button
                key={sandbox.id}
                className={`sandbox-item ${selected?.id === sandbox.id ? "is-active" : ""}`}
                onClick={() => setSelectedSandbox(sandbox.id)}
              >
                <span className="sandbox-id">{sandbox.id}</span>
                <span className={`status-pill status-${sandbox.status}`}>{sandbox.status}</span>
                <small>{sandbox.driver ?? "unknown"} / {sandbox.namespace ?? "default"}</small>
              </button>
            ))}
            {sandboxes.length === 0 ? <p className="empty-copy">No sandboxes discovered yet.</p> : null}
          </div>
        </aside>

        <section className="panel detail-panel">
          <div className="panel-header">
            <h2>Selected Sandbox</h2>
            <span>{selected?.id ?? "None selected"}</span>
          </div>
          {selected ? (
            <div className="detail-grid">
              <div>
                <label>Status</label>
                <strong>{selected.status}</strong>
              </div>
              <div>
                <label>Health</label>
                <strong>{selected.health_status ?? "--"}</strong>
              </div>
              <div>
                <label>Driver</label>
                <strong>{selected.driver ?? "--"}</strong>
              </div>
              <div>
                <label>Endpoint</label>
                <strong>{selected.rpc_endpoint ?? "--"}</strong>
              </div>
              <div>
                <label>TTL</label>
                <strong>{formatExpiry(selected.expires_at)}</strong>
              </div>
              <div>
                <label>Pod IP</label>
                <strong>{selected.metadata?.pod_ip ?? "--"}</strong>
              </div>
            </div>
          ) : (
            <p className="empty-copy">Select a sandbox to inspect live metadata.</p>
          )}
        </section>
      </section>

      <section className="timeline-layout">
        <section className="panel event-console">
          <div className="panel-header">
            <h2>Live Event Console</h2>
            <span>{filteredEvents.length} frames</span>
          </div>
          <div className="console-log">
            {filteredEvents.map((event, index) => (
              <article key={`${event.timestamp ?? "evt"}-${index}`} className="console-line">
                <span className="console-time">{formatTimestamp(event.timestamp)}</span>
                <span className="console-type">{event.type}</span>
                <span className="console-message">{event.message ?? event.method ?? "--"}</span>
              </article>
            ))}
            {filteredEvents.length === 0 ? <p className="empty-copy">Waiting for the first SSE frame.</p> : null}
          </div>
        </section>

        <section className="panel activity-rail">
          <div className="panel-header">
            <h2>Activity Rail</h2>
            <span>Most recent control-plane slices</span>
          </div>
          <div className="rail">
            {events.slice(0, 8).map((event, index) => (
              <div key={`${event.type}-${index}`} className="rail-item">
                <div className="rail-marker" />
                <div>
                  <strong>{event.type}</strong>
                  <p>{event.message ?? event.method ?? "event"}</p>
                  <small>{formatTimestamp(event.timestamp)}</small>
                </div>
              </div>
            ))}
          </div>
        </section>
      </section>

      {showSettings && (
        <div className="settings-overlay" onClick={() => setShowSettings(false)}>
          <div className="settings-modal" onClick={(e) => e.stopPropagation()}>
            <div className="panel-header">
              <h2>Settings</h2>
              <button className="close-btn" onClick={() => setShowSettings(false)}>×</button>
            </div>
            <div className="settings-content">
              <label>API Key</label>
              <input 
                type="password" 
                value={apiKey} 
                onChange={(e) => saveApiKey(e.target.value)} 
                placeholder="sk-..."
                className="settings-input"
              />
              <p className="settings-helper">Your API key is stored locally in your browser.</p>
            </div>
          </div>
        </div>
      )}
    </main>
  );
}
