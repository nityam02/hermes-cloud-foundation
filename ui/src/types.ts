// Shapes of the documents Hermes writes (docs/architecture/data-contracts.md). Only the fields the
// UI reads are typed; unknown fields are ignored.

export interface Gap {
  service: string;
  region: string;
  operation: string;
  reason: string;
  message: string;
  impact?: string;
}

export interface InventoryResource {
  id: string;
  type: string;
  name: string;
  region: string;
  managed_by?: string;
  completeness?: string;
}

export interface Relationship {
  from: string;
  to: string;
  type: string;
}

export interface Inventory {
  kind: "Inventory";
  metadata?: { collector?: string; read_only?: boolean; regions?: string[]; account_id?: string; generated_at?: string };
  resources: InventoryResource[];
  relationships?: Relationship[];
  gaps?: Gap[];
}

export type Compatibility = "equivalent" | "compatible" | "partial" | "re-architect" | "unsupported" | string;

export interface AssessedResource {
  id: string;
  type: string;
  name: string;
  region: string;
  category: string;
  target_service: string;
  alternatives?: string[];
  compatibility: Compatibility;
  complexity: "low" | "medium" | "high" | string;
  data_migration?: string;
  hermes_support?: string;
  wave: number;
  notes?: string[];
}

export interface Blocker {
  code: string;
  severity: "blocker" | "risk" | "info" | string;
  resource_ids: string[];
  message: string;
  recommendation: string;
}

export interface Unsupported {
  source_id: string;
  source_type: string;
  source_name: string;
  reason: string;
  recommended_service?: string;
  suggestion?: string;
}

export interface Assessment {
  kind: "Assessment";
  metadata?: { generated_at?: string; source?: { provider: string; regions?: string[] }; target?: { provider: string }; disclaimer?: string };
  summary: {
    resource_count: number;
    by_compatibility: Record<string, number>;
    by_complexity: Record<string, number>;
    portability_score: number;
    portability_method: string;
    data_volume?: { required_bytes: number; optional_bytes: number };
    transfer_cost?: { currency: string; low_usd: number; high_usd: number; method?: string };
    blocker_count: number;
    risk_count: number;
    gap_count: number;
    wave_count: number;
  };
  resources: AssessedResource[];
  blockers: Blocker[];
  gaps: Gap[];
  dependencies?: { waves: { index: number; resources: string[] }[] };
  unsupported?: Unsupported[];
}

export interface PlanStep {
  id: string;
  title: string;
  description: string;
  phase: string;
  wave: number;
  automation: "manual" | "hermes" | "provider-tool" | string;
  gate: "none" | "approval" | "validation" | string;
  source_impact: "none" | "read-only-load" | "modifies-source-config" | "modifies-source-data" | string;
  reversible: boolean;
  rollback?: string;
  validation?: string[];
  risks?: string[];
  resource_refs?: string[];
}

export interface PlanPhase {
  id: string;
  order: number;
  steps: PlanStep[];
}

export interface Plan {
  kind: "MigrationPlan";
  metadata: {
    plan_id: string;
    generated_at?: string;
    source: { provider: string; regions?: string[] };
    target: { provider: string; environment?: string };
  };
  summary: { phases: number; steps: number; approval_gates: number; validation_gates: number; source_impacting_steps: number; manual_steps: number };
  phases: PlanPhase[];
  notes?: string[];
}

export interface Docs {
  mode: "live" | "demo" | "empty";
  inventory?: Inventory;
  assessment?: Assessment;
  plan?: Plan;
}
