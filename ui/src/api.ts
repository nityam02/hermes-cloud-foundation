// Thin client for the `hermes ui` API. Every POST carries the custom header the server requires, so a
// page on another site could not make the same request. Credentials are the session cookie only.

export class AuthError extends Error {}

async function call<T>(method: "GET" | "POST", path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { Accept: "application/json" };
  if (method === "POST") {
    headers["Content-Type"] = "application/json";
    headers["X-Hermes-UI"] = "1";
  }
  const r = await fetch(path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body), credentials: "same-origin" });
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
    throw new Error(msg);
  }
  return data as T;
}

export const api = {
  get: <T>(path: string) => call<T>("GET", path),
  post: <T>(path: string, body: unknown) => call<T>("POST", path, body),
};

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
  state: "running" | "done" | "failed";
  steps: JobStep[];
  error?: string;
}

export interface Status {
  inventory: boolean;
  assessment: boolean;
  plan: boolean;
  version: string;
  can: { connect_aws: boolean; connect_gcp: boolean; discover: boolean };
}
