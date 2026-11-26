import { normalizeDocs } from "./lib";
import type { Assessment, Docs, Inventory, Plan } from "./types";

async function getJSON<T>(url: string): Promise<T | undefined> {
  try {
    const r = await fetch(url, { headers: { Accept: "application/json" } });
    if (!r.ok) return undefined;
    return (await r.json()) as T;
  } catch {
    return undefined;
  }
}

/** Loads the documents served by `hermes ui`; with ?demo (or when no server answers) it loads the synthetic sample. */
export async function loadDocs(search: string = window.location.search): Promise<Docs> {
  const wantDemo = new URLSearchParams(search).has("demo");
  if (!wantDemo) {
    const status = await getJSON<{ inventory: boolean; assessment: boolean; plan: boolean }>("api/status");
    if (status) {
      const [inventory, assessment, plan] = await Promise.all([
        status.inventory ? getJSON<Inventory>("api/inventory.json") : undefined,
        status.assessment ? getJSON<Assessment>("api/assessment.json") : undefined,
        status.plan ? getJSON<Plan>("api/plan.json") : undefined,
      ]);
      return normalizeDocs({ mode: inventory || assessment || plan ? "live" : "empty", inventory, assessment, plan });
    }
  }
  const [inventory, assessment, plan] = await Promise.all([
    getJSON<Inventory>("demo/inventory.json"),
    getJSON<Assessment>("demo/assessment.json"),
    getJSON<Plan>("demo/plan.json"),
  ]);
  return normalizeDocs({ mode: inventory || assessment || plan ? "demo" : "empty", inventory, assessment, plan });
}
