// Decisions the Connect page makes, kept apart from the views so they can be tested.
import type { CheckResult, Identity, RoleFind } from "./api";

/** The profile to start with: the remembered one, else the first that is signed in and usable. */
export function pickProfile(scan: Identity[], saved: string): string {
  if (saved && scan.some((p) => p.name === saved)) return saved;
  const usable = scan.find((p) => p.ok && !p.root);
  if (usable?.name) return usable.name;
  return "";
}

export function usable(i: Identity | undefined): boolean {
  return !!i && i.ok && !i.root;
}

/** One line about a profile's sign-in, for the profile list. */
export function profileState(i: Identity): { text: string; tone: "good" | "hard" | "none" } {
  if (i.ok && i.root) return { text: "Root user. AWS does not let it use roles.", tone: "hard" };
  if (i.ok) return { text: `Signed in as ${shortArn(i.arn ?? "")}`, tone: "good" };
  if (i.kind === "keys") return { text: "Access keys saved on this machine, not working.", tone: "hard" };
  return { text: "Not signed in", tone: "none" };
}

export const kindLabel: Record<string, string> = {
  login: "Console sign-in",
  sso: "IAM Identity Center",
  keys: "Access keys",
  role: "Assumes a role",
  process: "Helper program",
  other: "Not set up",
};

/** "arn:aws:iam::123:user/ops/alice" -> "user/ops/alice"; assumed roles keep role and session. */
export function shortArn(arn: string): string {
  const i = arn.indexOf(":", arn.indexOf("::") + 2);
  const rest = arn.includes("::") && i > 0 ? arn.slice(i + 1) : arn;
  return rest.replace(/^assumed-role\//, "role/");
}

export type RoleState = "loading" | "none" | "ready" | "other-principal" | "busy" | "broken" | "error";

export function roleState(find: RoleFind | null, me: string): RoleState {
  if (!find) return "loading";
  if (find.error) return "error";
  const s = find.stack;
  if (!find.found || !s) return "none";
  if (s.status.endsWith("_IN_PROGRESS")) return "busy";
  const complete = ["CREATE_COMPLETE", "UPDATE_COMPLETE", "UPDATE_ROLLBACK_COMPLETE", "IMPORT_COMPLETE", "IMPORT_ROLLBACK_COMPLETE"].includes(s.status);
  if (!complete || !s.role_arn) return "broken";
  if (s.trusted_principal && me && s.trusted_principal !== me) return "other-principal";
  return "ready";
}

/** What the Google check result asks the person to do next, as buttons. */
export function gcpFixes(r: CheckResult | null): { billing: boolean; apis: "required" | "recommended" | null } {
  if (!r) return { billing: false, apis: null };
  const billing = r.steps.some((s) => s.id === "billing" && s.status === "fail");
  const apis = r.steps.find((s) => s.id === "apis");
  let need: "required" | "recommended" | null = null;
  if (apis && (apis.status === "fail" || apis.status === "warn")) need = "required";
  else if (apis && /more are needed/.test(apis.detail ?? "")) need = "recommended";
  return { billing, apis: need };
}

/** Whether the AWS check found read calls the role may not make (its permissions are out of date). */
export function awsHasGaps(r: CheckResult | null): boolean {
  return !!r?.steps.some((s) => s.id === "access" && s.status === "warn");
}

export const gcpRegions = [
  "europe-north1", "europe-west1", "europe-west2", "europe-west3", "europe-west4", "europe-central2",
  "us-central1", "us-east1", "us-east4", "us-west1", "northamerica-northeast1", "southamerica-east1",
  "asia-northeast1", "asia-south1", "asia-southeast1", "australia-southeast1",
];

export const awsRegions = [
  "us-east-1", "us-east-2", "us-west-1", "us-west-2", "eu-north-1", "eu-west-1", "eu-west-2", "eu-west-3",
  "eu-central-1", "eu-south-1", "ap-northeast-1", "ap-southeast-1", "ap-southeast-2", "ap-south-1", "ca-central-1", "sa-east-1",
];

export function when(iso?: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return d.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}
