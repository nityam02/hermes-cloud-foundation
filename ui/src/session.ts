// Form values survive a reload through sessionStorage (cleared when the tab closes). Nothing secret is
// stored: the external ID is an anti-confusion value, not a credential, and cloud sign-ins stay in the
// operator's own tools.
export interface Session {
  awsProfile: string;
  awsRegion: string;
  awsArn: string;
  externalId: string;
  roleArn: string;
  awsOk: boolean;
  gcpProject: string;
  gcpRegion: string;
  gcpOk: boolean;
  regions: string;
}

export const defaults: Session = {
  awsProfile: "",
  awsRegion: "us-east-1",
  awsArn: "",
  externalId: "",
  roleArn: "",
  awsOk: false,
  gcpProject: "",
  gcpRegion: "europe-north1",
  gcpOk: false,
  regions: "",
};

const KEY = "hermes-ui-session";

export function loadSession(): Session {
  try {
    const raw = window.sessionStorage.getItem(KEY);
    if (raw) return { ...defaults, ...(JSON.parse(raw) as Partial<Session>) };
  } catch {
    /* storage unavailable: start fresh */
  }
  return { ...defaults };
}

export function saveSession(s: Session): void {
  try {
    window.sessionStorage.setItem(KEY, JSON.stringify(s));
  } catch {
    /* ignore */
  }
}

/** Splits "eu-north-1, us-east-1" into a clean list. */
export function parseRegions(text: string, fallback: string): string[] {
  const list = text
    .split(/[\s,]+/)
    .map((r) => r.trim())
    .filter(Boolean);
  return list.length ? [...new Set(list)] : [fallback];
}
