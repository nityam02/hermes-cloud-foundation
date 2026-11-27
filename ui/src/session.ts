// What the page remembers between runs of `hermes ui`. The server keeps it in
// .hermes/ui/connection.json. It holds names and identifiers only: the external ID is an
// anti-confusion value, not a credential, and cloud sign-ins stay in the clouds' own tools.
export interface Connection {
  aws: {
    profile: string;
    region: string;
    identity: string;
    account: string;
    role_arn: string;
    external_id: string;
    ok: boolean;
    checked?: string;
    summary?: string;
  };
  gcp: {
    account: string;
    project_id: string;
    region: string;
    ok: boolean;
    checked?: string;
    summary?: string;
  };
  regions: string;
}

export const defaults: Connection = {
  aws: { profile: "", region: "us-east-1", identity: "", account: "", role_arn: "", external_id: "", ok: false },
  gcp: { account: "", project_id: "", region: "europe-north1", ok: false },
  regions: "",
};

/** Fills in anything an older or partial file left out. */
export function withDefaults(c: Partial<Connection> | null | undefined): Connection {
  const aws = { ...defaults.aws, ...(c?.aws ?? {}) };
  const gcp = { ...defaults.gcp, ...(c?.gcp ?? {}) };
  // A connection that was never saved comes back with empty strings: keep the default regions.
  aws.region ||= defaults.aws.region;
  gcp.region ||= defaults.gcp.region;
  return { aws, gcp, regions: c?.regions ?? "" };
}

/** Splits "eu-north-1, us-east-1" into a clean list. */
export function parseRegions(text: string, fallback: string): string[] {
  const list = text
    .split(/[\s,]+/)
    .map((r) => r.trim())
    .filter(Boolean);
  return list.length ? [...new Set(list)] : [fallback];
}
