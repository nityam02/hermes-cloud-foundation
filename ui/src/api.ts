// Thin client for the `hermes ui` API. Every POST carries the custom header the server requires, so a
// page on another site could not make the same request. Credentials are the session cookie only.

export class AuthError extends Error {}

/** An error answer from the server, with its status and body. */
export class ApiError extends Error {
  constructor(message: string, readonly status: number, readonly data: unknown) {
    super(message);
  }
}

async function call<T>(method: "GET" | "POST", path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { Accept: "application/json" };
  if (method === "POST") {
    headers["Content-Type"] = "application/json";
    headers["X-Hermes-UI"] = "1";
  }
  let r: Response;
  try {
    r = await fetch(path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body), credentials: "same-origin" });
  } catch {
    throw new Error("Cannot reach the Hermes server. It has probably stopped. Run `hermes ui` again in your terminal and use the page it opens.");
  }
  if (r.status === 401) throw new AuthError("locked");
  const text = await r.text();
  let data: unknown = undefined;
  try {
    data = text ? JSON.parse(text) : undefined;
  } catch {
    /* not JSON */
  }
  if (!r.ok) {
    const msg = (data as { error?: string } | undefined)?.error ?? `The server answered ${r.status}.`;
    throw new ApiError(sentence(msg), r.status, data);
  }
  return data as T;
}

export const api = {
  get: <T>(path: string) => call<T>("GET", path),
  post: <T>(path: string, body: unknown) => call<T>("POST", path, body),
};

/** Go error strings start lower case; the page shows them as sentences. */
export function sentence(s: string): string {
  const t = s.trim();
  if (!t) return t;
  const first = t[0].toUpperCase() + t.slice(1);
  return /[.!?]$/.test(first) ? first : first + ".";
}

export type StepStatus = "pass" | "warn" | "fail" | "skip";

export interface CheckStep {
  id: string;
  title: string;
  status: StepStatus;
  detail?: string;
  fix?: string;
}

export interface CheckResult {
  ok: boolean;
  summary: string;
  steps: CheckStep[];
  identity?: string;
}

export interface JobStep {
  name: string;
  status: "running" | "done" | "failed" | "warn";
  detail?: string;
}

export interface Job {
  id: string;
  kind: string;
  state: "running" | "done" | "failed" | "cancelled";
  steps: JobStep[];
  error?: string;
  link?: string;
  code?: string;
  log?: string[];
  result?: unknown;
}

export interface Status {
  inventory: boolean;
  assessment: boolean;
  plan: boolean;
  version: string;
  can: Record<string, boolean>;
}

export interface AwsProfile {
  name: string;
  kind: "login" | "sso" | "keys" | "role" | "process" | "other";
}

export interface AwsCli {
  installed: boolean;
  version?: string;
  login: boolean;
}

export interface Identity {
  name?: string;
  kind?: AwsProfile["kind"];
  ok: boolean;
  arn?: string;
  account?: string;
  root?: boolean;
  error?: { detail: string; fix?: string };
}

export interface RoleStack {
  status: string;
  role_arn?: string;
  external_id?: string;
  trusted_principal?: string;
  reason?: string;
}

export interface RoleFind {
  found: boolean;
  stack?: RoleStack | null;
  error?: string;
}

export interface GcpState {
  gcloud: boolean;
  brew: boolean;
  signed_in: boolean;
  account?: string;
  problem?: string;
}

export interface GcpProject {
  id: string;
  name: string;
  number: string;
}
