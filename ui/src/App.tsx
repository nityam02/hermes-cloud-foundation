import { useCallback, useEffect, useState } from "react";
import { api, AuthError, type Status } from "./api";
import { loadDocs } from "./data";
import { loadSession, type Session } from "./session";
import type { Docs } from "./types";
import { Connect } from "./views/Connect";
import { Gaps } from "./views/Gaps";
import { Inventory } from "./views/Inventory";
import { Overview } from "./views/Overview";
import { PlanView } from "./views/Plan";

const tabs = [
  { id: "connect", label: "Connect" },
  { id: "overview", label: "Overview" },
  { id: "inventory", label: "Inventory" },
  { id: "plan", label: "Plan" },
  { id: "gaps", label: "Gaps" },
] as const;
type Tab = (typeof tabs)[number]["id"];

function tabFromHash(fallback: Tab): Tab {
  const h = window.location.hash.replace(/^#\/?/, "");
  return (tabs.find((t) => t.id === h)?.id ?? fallback) as Tab;
}

function Mark() {
  // A route with three stops: the product's one recurring image.
  return (
    <svg width="28" height="28" viewBox="0 0 28 28" aria-hidden="true" className="mark">
      <path d="M5 22 C 5 12, 23 16, 23 6" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" />
      <circle cx="5" cy="22" r="3.2" fill="var(--bg)" stroke="currentColor" strokeWidth="2.5" />
      <circle cx="14" cy="14.5" r="3.2" fill="var(--bg)" stroke="currentColor" strokeWidth="2.5" />
      <circle cx="23" cy="6" r="3.2" fill="currentColor" />
    </svg>
  );
}

export function App() {
  const [docs, setDocs] = useState<Docs | null>(null);
  const [status, setStatus] = useState<Status | null>(null);
  const [locked, setLocked] = useState(false);
  const [session, setSession] = useState<Session>(loadSession);
  const demo = new URLSearchParams(window.location.search).has("demo");
  const [tab, setTab] = useState<Tab>(() => tabFromHash(demo ? "overview" : "connect"));

  const refresh = useCallback(async () => {
    try {
      if (!demo) setStatus(await api.get<Status>("api/status"));
    } catch (e) {
      if (e instanceof AuthError) {
        setLocked(true);
        return;
      }
    }
    setDocs(await loadDocs());
  }, [demo]);

  useEffect(() => {
    void refresh();
    const onHash = () => setTab(tabFromHash("connect"));
    window.addEventListener("hashchange", onHash);
    return () => window.removeEventListener("hashchange", onHash);
  }, [refresh]);

  const hasResults = !!(docs?.assessment || docs?.plan || docs?.inventory);
  // First visit with documents already loaded (hermes ui --plan ...): start on the overview.
  useEffect(() => {
    if (docs && hasResults && !window.location.hash && !demo) window.location.hash = "#/overview";
  }, [docs, hasResults, demo]);

  if (locked) {
    return (
      <div className="shell">
        <div className="panel lock">
          <h1>This page is locked</h1>
          <p>Hermes UI only opens from the link that <code>hermes ui</code> printed in your terminal. That link carries a private session token.</p>
          <p className="muted">If you closed the terminal, run <code>hermes ui</code> again and use the new link.</p>
        </div>
      </div>
    );
  }

  const goOverview = () => {
    void refresh().then(() => {
      window.location.hash = "#/overview";
    });
  };

  return (
    <div className="shell">
      <header className="top">
        <div className="brand"><Mark />Hermes<small>cloud migration</small></div>
        <nav aria-label="Sections">
          {tabs.filter((t) => demo ? t.id !== "connect" : true).map((t) => (
            <a key={t.id} href={`#/${t.id}`} aria-current={tab === t.id ? "page" : undefined}>{t.label}</a>
          ))}
        </nav>
      </header>
      {docs?.mode === "demo" && (
        <p className="banner" role="status">
          Showing a synthetic sample, not a real account. Run <code>hermes ui</code> to connect your own.
        </p>
      )}
      <main>
        {!docs && <p className="muted">Loading…</p>}
        {docs && tab === "connect" && status && <Connect session={session} setSession={setSession} status={status} onDiscovered={goOverview} />}
        {docs && tab === "connect" && !status && <p className="muted">Connecting is available when you open the UI with <code>hermes ui</code>.</p>}
        {docs && tab === "overview" && <Overview docs={docs} />}
        {docs && tab === "inventory" && <Inventory docs={docs} />}
        {docs && tab === "plan" && <PlanView docs={docs} />}
        {docs && tab === "gaps" && <Gaps docs={docs} />}
      </main>
      <footer>
        Hermes reads your clouds through your own sign-in and never asks for access keys. This version only reads: it creates, changes and deletes nothing.
      </footer>
    </div>
  );
}
