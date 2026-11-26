import type { AssessedResource, Assessment, Docs, Inventory, Plan, PlanStep } from "./types";

/** Short, readable form of an ARN or id: the part after the last "/" or ":". */
export function shortId(id: string): string {
  const slash = id.lastIndexOf("/");
  if (slash >= 0 && slash < id.length - 1) return id.slice(slash + 1);
  const colon = id.lastIndexOf(":");
  return colon >= 0 && colon < id.length - 1 ? id.slice(colon + 1) : id;
}

/** "AWS::EC2::Subnet" -> "EC2 Subnet". */
export function typeLabel(t: string): string {
  return t.replace(/^AWS::/, "").replace(/::/g, " ");
}

export function formatBytes(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  let i = 0;
  let v = n;
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000;
    i++;
  }
  return `${v >= 100 || i === 0 ? Math.round(v) : v.toFixed(1)} ${units[i]}`;
}

export function formatUSD(n: number): string {
  return n.toLocaleString("en-US", { style: "currency", currency: "USD", maximumFractionDigits: n < 100 ? 2 : 0 });
}

/** Plain-language band for the portability score. */
export function scoreBand(score: number): { label: string; tone: "good" | "mixed" | "hard" } {
  if (score >= 75) return { label: "Mostly portable", tone: "good" };
  if (score >= 40) return { label: "Partly portable", tone: "mixed" };
  return { label: "Hard to port", tone: "hard" };
}

export interface ResourceFilter {
  text: string;
  type: string;
  region: string;
  compatibility: string;
}

export function filterResources(rs: AssessedResource[], f: ResourceFilter): AssessedResource[] {
  const q = f.text.trim().toLowerCase();
  return rs.filter(
    (r) =>
      (!f.type || r.type === f.type) &&
      (!f.region || r.region === f.region) &&
      (!f.compatibility || r.compatibility === f.compatibility) &&
      (!q || r.name.toLowerCase().includes(q) || r.type.toLowerCase().includes(q) || r.target_service.toLowerCase().includes(q)),
  );
}

export function uniqueSorted(values: string[]): string[] {
  return [...new Set(values)].sort();
}

export interface StepFilter {
  approvalOnly: boolean;
  sourceOnly: boolean;
}

export function touchesSource(s: PlanStep): boolean {
  return s.source_impact === "modifies-source-config" || s.source_impact === "modifies-source-data";
}

export function filterSteps(steps: PlanStep[], f: StepFilter): PlanStep[] {
  return steps.filter((s) => (!f.approvalOnly || s.gate === "approval") && (!f.sourceOnly || touchesSource(s)));
}

export function phaseTitle(id: string): string {
  return id.charAt(0).toUpperCase() + id.slice(1).replace(/-/g, " ");
}

/** Counts recomputed from the steps, so the header can be checked against the plan's own summary. */
export function planCounts(p: Plan): { steps: number; approvals: number; source: number } {
  const all = p.phases.flatMap((ph) => ph.steps);
  return { steps: all.length, approvals: all.filter((s) => s.gate === "approval").length, source: all.filter(touchesSource).length };
}

/** "aws" -> "AWS", "gcp" -> "GCP", "alibaba" -> "Alibaba Cloud". */
export function providerName(p: string | undefined, fallback: string): string {
  switch (p) {
    case "aws": return "AWS";
    case "gcp": return "GCP";
    case "alibaba": return "Alibaba Cloud";
    case undefined: case "": return fallback;
    default: return p;
  }
}

/**
 * Hermes is written in Go, which encodes an empty list as null and omits empty fields. Replace every
 * list the views read with an array so no view has to check for null.
 */
export function normalizeDocs(d: Docs): Docs {
  const inv = d.inventory as Inventory | undefined;
  if (inv) {
    inv.resources ??= [];
    inv.relationships ??= [];
    inv.gaps ??= [];
  }
  const a = d.assessment as Assessment | undefined;
  if (a) {
    a.resources ??= [];
    a.blockers ??= [];
    a.gaps ??= [];
    a.unsupported ??= [];
    a.summary.by_compatibility ??= {};
    a.summary.by_complexity ??= {};
    a.dependencies ??= { waves: [] };
    a.dependencies.waves ??= [];
    a.blockers.forEach((b) => (b.resource_ids ??= []));
    a.resources.forEach((r) => {
      r.notes ??= [];
      r.alternatives ??= [];
    });
    a.dependencies.waves.forEach((w) => (w.resources ??= []));
  }
  const p = d.plan as Plan | undefined;
  if (p) {
    p.phases ??= [];
    p.notes ??= [];
    p.phases.forEach((ph) => {
      ph.steps ??= [];
      ph.steps.forEach((st) => {
        st.validation ??= [];
        st.risks ??= [];
        st.resource_refs ??= [];
      });
    });
  }
  return d;
}
