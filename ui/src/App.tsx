import { useEffect, useState } from "react";
import { loadDocs } from "./data";
import type { Docs } from "./types";
import { Overview } from "./views/Overview";
import { Inventory } from "./views/Inventory";
import { PlanView } from "./views/Plan";
import { Gaps } from "./views/Gaps";

const tabs = [
  { id: "overview", label: "Overview" },
  { id: "inventory", label: "Inventory" },
  { id: "plan", label: "Plan" },
  { id: "gaps", label: "Gaps" },
] as const;
type Tab = (typeof tabs)[number]["id"];

function currentTab(): Tab {
  const h = window.location.hash.replace(/^#\/?/, "");
  return (tabs.find((t) => t.id === h)?.id ?? "overview") as Tab;
}

export function App() {
  const [docs, setDocs] = useState<Docs | null>(null);
  const [tab, setTab] = useState<Tab>(currentTab());

  useEffect(() => {
    void loadDocs().then(setDocs);
    const onHash = () => setTab(currentTab());
    window.addEventListener("hashchange", onHash);
    return () => window.removeEventListener("hashchange", onHash);
  }, []);

  return (
    <div className="shell">
      <header className="top">
        <div className="brand">
          Hermes<small>migration console, read-only</small>
        </div>
        <nav aria-label="Sections">
          {tabs.map((t) => (
            <a key={t.id} href={`#/${t.id}`} aria-current={tab === t.id ? "page" : undefined}>
              {t.label}
            </a>
          ))}
        </nav>
      </header>
      {docs?.mode === "demo" && (
        <p className="banner" role="status">
          Showing a synthetic sample, not a real account. Run <code>hermes ui --inventory … --assessment … --plan …</code> to see your own.
        </p>
      )}
      <main>
        {!docs && <p className="muted">Loading…</p>}
        {docs && tab === "overview" && <Overview docs={docs} />}
        {docs && tab === "inventory" && <Inventory docs={docs} />}
        {docs && tab === "plan" && <PlanView docs={docs} />}
        {docs && tab === "gaps" && <Gaps docs={docs} />}
      </main>
      <footer>
        This page only reads documents Hermes already wrote. It cannot plan, apply, approve or cut over anything, and it makes no requests to other sites.
      </footer>
    </div>
  );
}
