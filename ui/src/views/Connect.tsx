import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import {
  api,
  ApiError,
  sentence,
  type AwsCli,
  type AwsProfile,
  type CheckResult,
  type GcpProject,
  type GcpState,
  type Identity,
  type Job,
  type RoleFind,
  type RoleStack,
  type Status,
} from "../api";
import { Checklist, CopyButton, Field, Problems, StatusIcon } from "../components";
import { awsHasGaps, awsRegions, gcpFixes, gcpRegions, kindLabel, pickProfile, profileState, roleState, shortArn, usable, when } from "../connectLogic";
import { parseRegions, withDefaults, type Connection } from "../session";

type Props = { status: Status; onDiscovered: () => void };
type SetAws = (p: Partial<Connection["aws"]>) => void;
type SetGcp = (p: Partial<Connection["gcp"]>) => void;

/** The remembered connection, saved on the server a moment after each change. */
function useConnection() {
  const [conn, setConn] = useState<Connection | null>(null);
  const ref = useRef<Connection | null>(null);
  const timer = useRef<number | undefined>(undefined);
  useEffect(() => {
    void api.get<Connection>("api/connection").then(
      (c) => {
        ref.current = withDefaults(c);
        setConn(ref.current);
      },
      () => {
        ref.current = withDefaults(null);
        setConn(ref.current);
      },
    );
    return () => window.clearTimeout(timer.current);
  }, []);
  const update = useCallback((fn: (c: Connection) => Connection) => {
    if (!ref.current) return;
    const next = fn(ref.current);
    ref.current = next;
    setConn(next);
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => void api.post("api/connection", next).catch(() => undefined), 250);
  }, []);
  const setAws: SetAws = useCallback((p) => update((c) => ({ ...c, aws: { ...c.aws, ...p } })), [update]);
  const setGcp: SetGcp = useCallback((p) => update((c) => ({ ...c, gcp: { ...c.gcp, ...p } })), [update]);
  const setRegions = useCallback((regions: string) => update((c) => ({ ...c, regions })), [update]);
  return { conn, setAws, setGcp, setRegions };
}

/** The job running in one lane (aws, gcp or discover). A reloaded page picks up a job still running. */
function useLaneJob(lane: "aws" | "gcp" | "discover", onFinish: (j: Job) => void) {
  const [job, setJob] = useState<Job | null>(null);
  const [error, setError] = useState<string | null>(null);
  const timer = useRef<number | undefined>(undefined);
  const finish = useRef(onFinish);
  finish.current = onFinish;

  const poll = useCallback((id: string) => {
    window.clearInterval(timer.current);
    const tick = () =>
      api.get<Job>(`api/jobs/${id}`).then(
        (raw) => {
          const j: Job = { ...raw, steps: raw.steps ?? [], log: raw.log ?? [] };
          setJob(j);
          if (j.state !== "running") {
            window.clearInterval(timer.current);
            finish.current(j);
          }
        },
        () => undefined,
      );
    void tick();
    timer.current = window.setInterval(() => void tick(), 900);
  }, []);

  useEffect(() => {
    void api.get<Record<string, string>>("api/jobs/running").then((r) => r[lane] && poll(r[lane]), () => undefined);
    return () => window.clearInterval(timer.current);
  }, [lane, poll]);

  const start = async (path: string, body: unknown = {}) => {
    setError(null);
    try {
      const { id } = await api.post<{ id: string }>(path, body);
      setJob({ id, kind: "", state: "running", steps: [] });
      poll(id);
    } catch (e) {
      const running = e instanceof ApiError && e.status === 409 ? (e.data as { id?: string } | undefined)?.id : undefined;
      if (running) poll(running);
      else setError(e instanceof Error ? e.message : String(e));
    }
  };
  const cancel = () => {
    if (job) void api.post(`api/jobs/${job.id}/cancel`, {}).catch(() => undefined);
  };
  const running = job?.state === "running";
  return { job, running, start, cancel, error, setError, clear: () => setJob(null) };
}

export function Connect({ status, onDiscovered }: Props) {
  const { conn, setAws, setGcp, setRegions } = useConnection();
  if (!conn) return <p className="muted">Loading…</p>;
  return <ConnectBoard conn={conn} setAws={setAws} setGcp={setGcp} setRegions={setRegions} status={status} onDiscovered={onDiscovered} />;
}

/** Both clouds side by side, then discovery. Exported for tests. */
export function ConnectBoard({ conn, setAws, setGcp, setRegions, status, onDiscovered }: Props & { conn: Connection; setAws: SetAws; setGcp: SetGcp; setRegions: (r: string) => void }) {
  const awsReady = conn.aws.ok;
  const gcpReady = conn.gcp.ok;
  return (
    <section className="connect">
      <h1>Connect your clouds</h1>
      <p className="lede">
        Sign in to both sides with your own accounts, right here. Hermes reads AWS and checks that Google Cloud is ready. It changes something only when you press a button that says what will change.
      </p>
      <div className={`bridge${awsReady ? " aws-ready" : ""}${gcpReady ? " gcp-ready" : ""}`}>
        <AwsSide conn={conn} setAws={setAws} can={status.can} />
        <div className="bridge-line" aria-hidden="true"><span /></div>
        <GcpSide conn={conn} setGcp={setGcp} can={status.can} />
      </div>
      <DiscoverPanel conn={conn} setRegions={setRegions} canRun={!!status.can.discover} onDone={onDiscovered} />
    </section>
  );
}

type LegState = "locked" | "current" | "done" | "fail";

function Leg({ n, title, state, summary, action, children }: { n: number; title: string; state: LegState; summary?: ReactNode; action?: ReactNode; children?: ReactNode }) {
  const said = { locked: "not yet", current: "to do", done: "done", fail: "needs attention" }[state];
  return (
    <li className={`leg ${state}`}>
      <span className="leg-dot" aria-hidden="true">{state === "done" ? "✓" : state === "fail" ? "!" : n}</span>
      <div className="leg-main">
        <div className="leg-head">
          <h3>
            {title} <span className="sr-only">({said})</span>
          </h3>
          {action}
        </div>
        {summary && <div className="leg-summary">{summary}</div>}
        {children && <div className="leg-body">{children}</div>}
      </div>
    </li>
  );
}

function CloudCard({ side, name, ready, children }: { side: "from" | "to"; name: string; ready: boolean; children: ReactNode }) {
  return (
    <article className={`cloud-card ${side}`} aria-label={`${side === "from" ? "From" : "To"} ${name}`}>
      <header>
        <p className="side">{side === "from" ? "From" : "To"}</p>
        <h2>{name}</h2>
        <span className={`chip ${ready ? "good" : ""}`}>{ready ? "Connected" : "Not connected"}</span>
      </header>
      <ol className="legs">{children}</ol>
    </article>
  );
}

function runningStep(j: Job | null) {
  if (!j) return undefined;
  for (let i = j.steps.length - 1; i >= 0; i--) if (j.steps[i].status === "running") return j.steps[i];
  return j.steps.at(-1);
}

/** A sign-in or setup in progress: what to do now, the fallback link, the device code, and Cancel. */
function Waiting({ job, onCancel, fallback }: { job: Job; onCancel?: () => void; fallback: string }) {
  const link = job.link && job.link.startsWith("https://") ? job.link : undefined;
  return (
    <div className="waiting" role="status">
      <span className="pulse" aria-hidden="true" />
      <div>
        <strong>{runningStep(job)?.detail ?? fallback}</strong>
        {job.code && (
          <p>
            Code to confirm: <span className="code-big">{job.code}</span>
          </p>
        )}
        {link && (
          <p>
            No new tab? <a href={link} target="_blank" rel="noreferrer">Open the sign-in page</a>.
          </p>
        )}
        {onCancel && (
          <button type="button" className="btn small" onClick={onCancel}>
            Cancel
          </button>
        )}
      </div>
    </div>
  );
}

function JobSteps({ job }: { job: Job }) {
  return (
    <ol className="checklist compact" aria-live="polite">
      {job.steps.map((s) => (
        <li key={s.name} className={s.status === "done" ? "pass" : s.status === "failed" ? "fail" : s.status === "warn" ? "warn" : "skip"}>
          <StatusIcon status={s.status} />
          <div>{s.detail}</div>
        </li>
      ))}
    </ol>
  );
}

function JobError({ job, error }: { job: Job | null; error: string | null }) {
  const msg = error ?? (job && (job.state === "failed" || job.state === "cancelled") ? job.error : null);
  if (!msg) return null;
  return (
    <div className={`callout ${job?.state === "cancelled" ? "" : "fail"}`} role="alert">
      {sentence(msg)}
    </div>
  );
}

function AwsSide({ conn, setAws, can }: { conn: Connection; setAws: SetAws; can: Record<string, boolean> }) {
  const a = conn.aws;
  const [cli, setCli] = useState<AwsCli | null>(null);
  const [scan, setScan] = useState<Identity[] | null>(null);
  const [kinds, setKinds] = useState<Record<string, AwsProfile["kind"]>>({});
  const [changing, setChanging] = useState(false);
  const [choice, setChoice] = useState("");
  const [newName, setNewName] = useState("hermes");
  const [find, setFind] = useState<RoleFind | null>(null);
  const [check, setCheck] = useState<CheckResult | null>(null);
  const [checking, setChecking] = useState(false);
  const [checkErr, setCheckErr] = useState<string | null>(null);
  const [manage, setManage] = useState(false);
  const [confirmRemove, setConfirmRemove] = useState(false);
  const pending = useRef("");
  const autoChecked = useRef("");
  const aRef = useRef(a);
  aRef.current = a;

  const rescan = useCallback(async (): Promise<Identity[]> => {
    const [p, s] = await Promise.all([
      api.get<{ profiles: AwsProfile[]; cli: AwsCli }>("api/connect/aws/profiles"),
      api.post<{ profiles: Identity[] }>("api/connect/aws/scan", { region: aRef.current.region }),
    ]);
    setCli(p.cli);
    setKinds(Object.fromEntries((p.profiles ?? []).map((x) => [x.name, x.kind])));
    const list = s.profiles ?? [];
    setScan(list);
    return list;
  }, []);

  const adopt = useCallback(
    (name: string, list: Identity[]) => {
      const i = list.find((p) => p.name === name);
      if (usable(i)) {
        const same = i!.arn === aRef.current.identity && name === aRef.current.profile;
        setAws({ profile: name, identity: i!.arn!, account: i!.account ?? "", ...(same ? {} : { ok: false, role_arn: "", checked: "", summary: "" }) });
        setChanging(false);
      }
    },
    [setAws],
  );

  // First look: which sign-ins work on this machine.
  useEffect(() => {
    void rescan().then(
      (list) => {
        const pick = pickProfile(list, aRef.current.profile);
        const fallback = list.find((p) => (p.kind === "login" || p.kind === "sso") && !p.root)?.name ?? "new";
        setChoice(pick || fallback);
        if (pick) adopt(pick, list);
        else setChanging(true);
      },
      () => setScan([]),
    );
  }, [rescan, adopt]);

  const me = scan?.find((p) => p.name === a.profile);
  const signedIn = usable(me) && !!a.identity && !changing;

  const job = useLaneJob("aws", (j) => {
    if (j.kind === "aws-signin" && j.state === "done") {
      void rescan().then((list) => adopt(pending.current, list));
    }
    if (j.kind === "aws-role" && j.state === "done") {
      const st = j.result as RoleStack;
      setFind({ found: true, stack: st });
      setAws({ role_arn: st.role_arn ?? "", external_id: st.external_id ?? "", ok: false, checked: "", summary: "" });
      autoChecked.current = "";
    }
    if (j.kind === "aws-role-remove" && j.state === "done") {
      setFind({ found: false });
      setAws({ role_arn: "", ok: false, checked: "", summary: "" });
      setCheck(null);
      setConfirmRemove(false);
      setManage(false);
    }
  });

  // Once signed in: look for the role.
  const lookForRole = useCallback(() => {
    setFind(null);
    void api.post<RoleFind>("api/connect/aws/role", { profile: aRef.current.profile, region: aRef.current.region }).then(
      (r) => {
        setFind(r);
        const st = r.stack;
        if (r.found && st?.role_arn && (st.role_arn !== aRef.current.role_arn || st.external_id !== aRef.current.external_id)) {
          setAws({ role_arn: st.role_arn, external_id: st.external_id ?? "", ok: false });
        }
      },
      (e: Error) => setFind({ found: false, error: e.message }),
    );
  }, [setAws]);
  useEffect(() => {
    if (signedIn && can.aws_role) lookForRole();
  }, [signedIn, a.identity, a.region, can.aws_role, lookForRole]);

  const rs = roleState(find, a.identity);

  const runCheck = useCallback(async () => {
    setChecking(true);
    setCheckErr(null);
    try {
      const cur = aRef.current;
      const r = await api.post<CheckResult>("api/connect/aws/check", { profile: cur.profile, region: cur.region, role_arn: cur.role_arn, external_id: cur.external_id });
      setCheck(r);
      const read = r.steps.find((s) => s.id === "read")?.detail ?? "";
      setAws({ ok: r.ok, checked: r.ok ? new Date().toISOString() : "", summary: r.ok ? read : "" });
    } catch (e) {
      setCheckErr(e instanceof Error ? e.message : String(e));
    } finally {
      setChecking(false);
    }
  }, [setAws]);

  // The role is ready and this role was not checked yet: check it now.
  useEffect(() => {
    if (rs === "ready" && a.role_arn && !(a.ok && a.checked) && autoChecked.current !== a.role_arn && !checking) {
      autoChecked.current = a.role_arn;
      void runCheck();
    }
  }, [rs, a.role_arn, a.ok, a.checked, checking, runCheck]);

  const signInBusy = job.running && job.job?.kind === "aws-signin";
  const roleBusy = job.running && (job.job?.kind === "aws-role" || job.job?.kind === "aws-role-remove");
  const chosen = scan?.find((p) => p.name === choice);
  const chosenKind = choice === "new" ? "new" : (chosen?.kind ?? kinds[choice] ?? "other");
  const ensure = () => job.start("api/connect/aws/role/ensure", { profile: a.profile, region: a.region, principal: a.identity, external_id: a.external_id });

  // 1. Sign in
  let signinBody: ReactNode = null;
  if (scan === null) signinBody = <p className="muted">Looking for AWS sign-ins on this machine…</p>;
  else if (signInBusy && job.job) signinBody = <Waiting job={job.job} onCancel={job.cancel} fallback="Starting the AWS sign-in…" />;
  else if (!signedIn) {
    const loginBlocked = cli && (!cli.installed || !cli.login);
    signinBody = (
      <>
        {cli && !cli.installed && (
          <div className="callout fail">
            <strong>The AWS CLI is not installed.</strong>
            <p>
              Hermes signs in through it, so it never sees your password. <a href="https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html" target="_blank" rel="noreferrer">Install the AWS CLI</a> (version 2.32 or later), then <button type="button" className="linkish" onClick={() => void rescan()}>check again</button>.
            </p>
          </div>
        )}
        {cli?.installed && !cli.login && (
          <div className="callout fail">
            <strong>Your AWS CLI ({cli.version ?? "old version"}) cannot sign in to the console.</strong>
            <p>Update it to version 2.32 or later, then check again. Profiles that are already signed in still work.</p>
          </div>
        )}
        <fieldset className="choices">
          <legend>Choose how Hermes signs in to AWS</legend>
          {scan.map((p) => {
            const st = profileState(p);
            return (
              <label key={p.name} className={`choice${choice === p.name ? " on" : ""}`}>
                <input type="radio" name="aws-profile" value={p.name} checked={choice === p.name} onChange={() => setChoice(p.name ?? "")} />
                <span className="choice-main">
                  <span className="choice-name">{p.name}</span>
                  <span className={`choice-state ${st.tone}`}>{st.text}</span>
                </span>
                <span className="choice-kind">{kindLabel[p.kind ?? "other"]}</span>
              </label>
            );
          })}
          <label className={`choice${choice === "new" ? " on" : ""}`}>
            <input type="radio" name="aws-profile" value="new" checked={choice === "new"} onChange={() => setChoice("new")} />
            <span className="choice-main">
              <span className="choice-name">New sign-in</span>
              <span className="choice-state">Sign in to the AWS console as an IAM user or SSO identity.</span>
            </span>
          </label>
        </fieldset>
        {choice === "new" && (
          <Field label="Name for this sign-in" hint="Saved as a profile in your AWS CLI config.">
            <input value={newName} onChange={(e) => setNewName(e.target.value.replace(/[^A-Za-z0-9_.+@-]/g, ""))} spellCheck={false} />
          </Field>
        )}
        {chosen?.root && (
          <p className="note">
            AWS never lets the root user use a role. Choose <b>New sign-in</b>, and in the browser sign in as an IAM user. No IAM user yet? Create one in the AWS console under IAM, Users.
          </p>
        )}
        <div className="row-actions">
          {usable(chosen) ? (
            <button type="button" className="btn primary" onClick={() => adopt(choice, scan)}>Use this sign-in</button>
          ) : chosenKind === "keys" || chosenKind === "role" || chosenKind === "process" ? (
            <p className="note">This profile does not sign in through the browser. Choose another, or a new sign-in.</p>
          ) : (
            <button
              type="button"
              className="btn primary"
              disabled={!can.aws_signin || !!loginBlocked || (choice === "new" && !newName)}
              onClick={() => {
                pending.current = choice === "new" ? newName : choice;
                void job.start("api/connect/aws/signin", { profile: pending.current, region: a.region });
              }}
            >
              Sign in to AWS
            </button>
          )}
          <label className="inline-field">
            Region
            <select value={a.region} onChange={(e) => setAws({ region: e.target.value })}>
              {[...new Set([a.region, ...awsRegions])].map((r) => <option key={r} value={r}>{r}</option>)}
            </select>
          </label>
          {changing && usable(me) && (
            <button type="button" className="btn" onClick={() => setChanging(false)}>Keep {a.profile}</button>
          )}
        </div>
        {job.job?.kind === "aws-signin" && <JobError job={job.job} error={null} />}
        {!job.job && <JobError job={null} error={job.error} />}
      </>
    );
  }

  // 2. Role
  let roleBody: ReactNode = null;
  let roleSummary: ReactNode = null;
  const roleDone = signedIn && rs === "ready" && !roleBusy;
  if (!signedIn) roleSummary = <span className="muted">After you sign in.</span>;
  else if (roleBusy && job.job) roleBody = <><JobSteps job={job.job} /></>;
  else if (rs === "loading") roleBody = <p className="muted">Looking for the Hermes role in this account…</p>;
  else if (rs === "ready") {
    roleSummary = (
      <>
        <span className="mono">HermesDiscoveryReadOnly</span> can read account {a.account}.
      </>
    );
    roleBody = manage ? (
      <div className="manage">
        <p>The role lives in the CloudFormation stack <span className="mono">hermes-discovery-role</span> in {a.region}. Its external ID is <span className="mono">{a.external_id}</span>.</p>
        {!confirmRemove ? (
          <div className="row-actions">
            <button type="button" className="btn" disabled={!can.aws_role} onClick={() => void ensure()}>Update permissions</button>
            <button type="button" className="btn danger" disabled={!can.aws_role} onClick={() => setConfirmRemove(true)}>Remove the role</button>
          </div>
        ) : (
          <div className="confirm" role="alertdialog" aria-label="Remove the role">
            <p><strong>Remove the role from account {a.account}?</strong> Hermes will not be able to read this account until you create it again. Nothing else in the account changes.</p>
            <div className="row-actions">
              <button type="button" className="btn danger" onClick={() => void job.start("api/connect/aws/role/remove", { profile: a.profile, region: a.region })}>Remove it</button>
              <button type="button" className="btn" onClick={() => setConfirmRemove(false)}>Keep it</button>
            </div>
          </div>
        )}
      </div>
    ) : null;
  } else if (rs === "outdated") {
    roleBody = (
      <>
        <p>
          This account has the Hermes role from an earlier setup, made with an older template that hides its external ID, so Hermes cannot use it yet. Updating gives it a new external ID and the latest read-only permissions.
        </p>
        <div className="row-actions">
          <button type="button" className="btn primary" disabled={!can.aws_role} onClick={() => void ensure()}>Update the role</button>
        </div>
      </>
    );
  } else if (rs === "none" || rs === "error") {
    roleBody = (
      <>
        {rs === "error" && find?.denied && <SetupDenied account={a.account} onRetry={lookForRole} />}
        {rs === "error" && !find?.denied && <div className="callout fail">{sentence(find?.error ?? "")}</div>}
        <p>
          Hermes reads AWS through a role of its own. The role can list and describe resources but cannot read secrets, files or table data, and only your sign-in can use it.
        </p>
        <div className="row-actions">
          <button type="button" className="btn primary" disabled={!can.aws_role} onClick={() => void ensure()}>Create the role</button>
        </div>
        <details>
          <summary>What this creates</summary>
          <dl className="kv">
            <dt>Stack</dt><dd><span className="mono">hermes-discovery-role</span> in {a.region}</dd>
            <dt>Role</dt><dd><span className="mono">HermesDiscoveryReadOnly</span>, read-only, sessions of one hour at most</dd>
            <dt>Who can use it</dt><dd><span className="mono">{a.identity}</span>, with an external ID Hermes generates</dd>
          </dl>
          <p className="muted">You can remove it from this page at any time.</p>
        </details>
        <ManualRole conn={conn} setAws={setAws} onUse={() => setFind({ found: true, stack: { status: "CREATE_COMPLETE", role_arn: aRef.current.role_arn, external_id: aRef.current.external_id, trusted_principal: a.identity } })} />
      </>
    );
  } else if (rs === "other-principal") {
    roleBody = (
      <>
        <p>
          This account already has the Hermes role, but it trusts <span className="mono">{shortArn(find?.stack?.trusted_principal ?? "")}</span>, not your sign-in.
        </p>
        <div className="row-actions">
          <button type="button" className="btn primary" disabled={!can.aws_role} onClick={() => void ensure()}>Trust my sign-in instead</button>
        </div>
      </>
    );
  } else if (rs === "busy") {
    roleBody = (
      <>
        <p>CloudFormation is still changing the role ({find?.stack?.status.toLowerCase().replaceAll("_", " ")}).</p>
        <button type="button" className="btn" onClick={lookForRole}>Look again</button>
      </>
    );
  } else if (rs === "broken") {
    roleBody = (
      <>
        <p>The role stack is in the state {find?.stack?.status}. {find?.stack?.reason}</p>
        <button type="button" className="btn primary" disabled={!can.aws_role} onClick={() => void ensure()}>Repair the role</button>
      </>
    );
  }
  const roleJobFailed = job.job && (job.job.kind === "aws-role" || job.job.kind === "aws-role-remove") && job.job.state !== "done" && job.job.state !== "running";

  // 3. Check
  let checkBody: ReactNode = null;
  let checkSummary: ReactNode = null;
  if (!roleDone) checkSummary = <span className="muted">After the role is ready.</span>;
  else if (checking) checkBody = <div className="waiting" role="status"><span className="pulse" aria-hidden="true" /><strong>Using the role to read {a.region}. This takes up to a minute.</strong></div>;
  else if (check && !check.ok) checkBody = <><Problems steps={check.steps} /><button type="button" className="btn" onClick={() => void runCheck()}>Check again</button></>;
  else if (a.ok) {
    checkSummary = <>{a.summary || "Hermes can read this account."} {a.checked && <span className="muted">Checked {when(a.checked)}.</span>}</>;
    checkBody = (
      <>
        {awsHasGaps(check) && (
          <p className="note">
            Some read calls were denied, so parts of the account would be missing from the plan. <button type="button" className="linkish" onClick={() => void ensure()}>Update the role's permissions</button>.
          </p>
        )}
        {check && (
          <details>
            <summary>Show the checks</summary>
            <Checklist steps={check.steps} />
          </details>
        )}
      </>
    );
  }

  return (
    <CloudCard side="from" name="AWS" ready={a.ok}>
      <Leg
        n={1}
        title="Sign in"
        state={signedIn ? "done" : "current"}
        summary={signedIn && <><span className="mono">{shortArn(a.identity)}</span> in account {a.account}</>}
        action={signedIn && <button type="button" className="btn small" onClick={() => { setChoice(a.profile); setChanging(true); }}>Change</button>}
      >
        {signinBody}
      </Leg>
      <Leg
        n={2}
        title="Read-only role"
        state={!signedIn ? "locked" : roleDone ? "done" : rs === "error" || rs === "broken" || roleJobFailed ? "fail" : "current"}
        summary={roleSummary}
        action={roleDone && <button type="button" className="btn small" aria-expanded={manage} onClick={() => { setManage(!manage); setConfirmRemove(false); }}>{manage ? "Close" : "Manage"}</button>}
      >
        {roleBody}
        {roleJobFailed && (job.job?.error ?? "").startsWith("your sign-in is not allowed") ? (
          <SetupDenied account={a.account} onRetry={lookForRole} />
        ) : (
          roleJobFailed && <JobError job={job.job} error={null} />
        )}
        {signedIn && job.error && !signInBusy && <JobError job={null} error={job.error} />}
      </Leg>
      <Leg
        n={3}
        title="Check access"
        state={!roleDone ? "locked" : a.ok ? "done" : check && !check.ok ? "fail" : "current"}
        summary={checkSummary}
        action={roleDone && a.ok && !checking && <button type="button" className="btn small" onClick={() => void runCheck()}>Check again</button>}
      >
        {checkBody}
        {checkErr && <div className="callout fail">{checkErr}</div>}
      </Leg>
    </CloudCard>
  );
}

/** The sign-in may not manage the role stack: show the exact policy an administrator can attach. */
function SetupDenied({ account, onRetry }: { account: string; onRetry: () => void }) {
  const [policy, setPolicy] = useState<string | null>(null);
  useEffect(() => {
    void api.post<{ policy: string }>("api/connect/aws/setup-policy", { account }).then((r) => setPolicy(r.policy), () => undefined);
  }, [account]);
  return (
    <div className="callout fail" role="alert">
      <strong>Your sign-in is not allowed to manage the Hermes role in this account.</strong>
      <p>
        An administrator can attach this policy to your sign-in (IAM, your user or role, Add permissions, Create inline policy, JSON). It only covers the stack <span className="mono">hermes-discovery-role</span> and the role <span className="mono">HermesDiscoveryReadOnly</span>. Or create the role yourself, below.
      </p>
      {policy && (
        <details>
          <summary>Show the policy</summary>
          <pre className="code">{policy}</pre>
          <CopyButton text={policy} label="Copy policy" />
        </details>
      )}
      <div className="row-actions">
        <button type="button" className="btn small" onClick={onRetry}>Look again</button>
      </div>
    </div>
  );
}

type Template = { external_id: string; template: string; command: string; console_url: string };

/** The do-it-yourself path: download the template, create the stack in the console, paste the ARN. */
function ManualRole({ conn, setAws, onUse }: { conn: Connection; setAws: SetAws; onUse: () => void }) {
  const a = conn.aws;
  const [tpl, setTpl] = useState<Template | null>(null);
  const [arn, setArn] = useState("");
  const [ext, setExt] = useState("");
  const load = () => {
    if (tpl) return;
    void api.post<Template>("api/connect/aws/template", { principal: a.identity, region: a.region, externalId: a.external_id }).then((t) => {
      setTpl(t);
      setExt(t.external_id);
    }, () => undefined);
  };
  const download = () => {
    if (!tpl) return;
    const url = URL.createObjectURL(new Blob([tpl.template], { type: "application/json" }));
    const el = document.createElement("a");
    el.href = url;
    el.download = "hermes-discovery-role.json";
    el.click();
    URL.revokeObjectURL(url);
  };
  return (
    <details onToggle={(e) => (e.currentTarget as HTMLDetailsElement).open && load()}>
      <summary>Create it yourself instead</summary>
      {!tpl ? (
        <p className="muted">Preparing the template…</p>
      ) : (
        <ol className="howto">
          <li><button type="button" className="btn small" onClick={download}>Download the template</button></li>
          <li>Open <a href={tpl.console_url} target="_blank" rel="noreferrer">CloudFormation, create stack</a>, choose <b>Upload a template file</b> and pick the file.</li>
          <li>
            Enter these values, acknowledge that the stack creates IAM resources, and create it:
            <dl className="kv">
              <dt>ExternalId</dt><dd><span className="mono">{tpl.external_id}</span> <CopyButton text={tpl.external_id} /></dd>
              <dt>TrustedPrincipalArn</dt><dd><span className="mono">{a.identity}</span> <CopyButton text={a.identity} /></dd>
            </dl>
          </li>
          <li>
            Paste the stack's <b>RoleArn</b> output. If the role already existed, enter the external ID it was made with instead.
            <div className="row-actions">
              <input className="grow" value={arn} onChange={(e) => setArn(e.target.value.trim())} placeholder="arn:aws:iam::123456789012:role/HermesDiscoveryReadOnly" spellCheck={false} aria-label="Role ARN" />
            </div>
            <div className="row-actions">
              <input className="grow" value={ext} onChange={(e) => setExt(e.target.value.trim())} spellCheck={false} aria-label="External ID" />
              <button type="button" className="btn" disabled={!/^arn:aws[a-zA-Z-]*:iam::\d{12}:role\//.test(arn) || ext.length < 16} onClick={() => { setAws({ role_arn: arn, external_id: ext, ok: false }); onUse(); }}>Use this role</button>
            </div>
          </li>
        </ol>
      )}
    </details>
  );
}

function GcpSide({ conn, setGcp, can }: { conn: Connection; setGcp: SetGcp; can: Record<string, boolean> }) {
  const g = conn.gcp;
  const [state, setState] = useState<GcpState | null>(null);
  const [projects, setProjects] = useState<GcpProject[] | null>(null);
  const [projErr, setProjErr] = useState<string | null>(null);
  const [changingProject, setChangingProject] = useState(!g.project_id);
  const [typed, setTyped] = useState(g.project_id);
  const [check, setCheck] = useState<CheckResult | null>(null);
  const [checking, setChecking] = useState(false);
  const gRef = useRef(g);
  gRef.current = g;

  const refresh = useCallback(async () => {
    const s = await api.get<GcpState>("api/connect/gcp/status");
    setState(s);
    if (s.signed_in) {
      const acct = s.account ?? "";
      if (acct && acct !== gRef.current.account) setGcp({ account: acct, ...(gRef.current.account ? { ok: false } : {}) });
      void api.get<{ projects: GcpProject[]; error?: string }>("api/connect/gcp/projects").then((r) => {
        setProjects(r.projects ?? []);
        setProjErr(r.error ? sentence(r.error) : null);
      }, (e: Error) => setProjErr(e.message));
    }
    return s;
  }, [setGcp]);

  useEffect(() => {
    void refresh().catch(() => setState({ gcloud: false, brew: false, signed_in: false }));
  }, [refresh]);

  const runCheck = useCallback(async (project?: string) => {
    const cur = gRef.current;
    const id = project ?? cur.project_id;
    if (!id) return;
    setChecking(true);
    try {
      const r = await api.post<CheckResult>("api/connect/gcp/check", { project_id: id, region: cur.region });
      setCheck(r);
      setGcp({ ok: r.ok, checked: r.ok ? new Date().toISOString() : "", summary: r.ok ? (r.steps.find((s) => s.id === "project")?.detail ?? "") : "" });
    } catch (e) {
      setCheck({ ok: false, summary: "", steps: [{ id: "x", title: "Check", status: "fail", detail: e instanceof Error ? e.message : String(e) }] });
    } finally {
      setChecking(false);
    }
  }, [setGcp]);

  const job = useLaneJob("gcp", (j) => {
    if (j.state !== "done") return;
    if (j.kind === "gcp-install" || j.kind === "gcp-signin") void refresh();
    if (j.kind === "gcp-apis") void runCheck();
  });

  const signedIn = !!state?.signed_in;
  const busyKind = job.running ? job.job?.kind : undefined;
  const projectDone = signedIn && !!g.project_id && !changingProject;
  const fixes = gcpFixes(check);

  const chooseProject = (id: string) => {
    if (!id) return;
    setGcp({ project_id: id, ok: false, checked: "", summary: "" });
    setChangingProject(false);
    void runCheck(id);
  };

  // 1. Sign in
  let signinBody: ReactNode = null;
  if (!state) signinBody = <p className="muted">Looking for the Google Cloud CLI and your sign-in…</p>;
  else if ((busyKind === "gcp-signin" || busyKind === "gcp-install") && job.job) {
    signinBody = (
      <>
        <Waiting job={job.job} onCancel={job.cancel} fallback="Working…" />
        {busyKind === "gcp-install" && job.job.log && job.job.log.length > 0 && <pre className="code log">{job.job.log.slice(-4).join("\n")}</pre>}
      </>
    );
  } else if (!state.gcloud) {
    signinBody = (
      <>
        <p>Hermes signs in to Google through the Google Cloud CLI, so it never sees your password. The CLI is not installed on this machine.</p>
        <div className="row-actions">
          {state.brew ? (
            <button type="button" className="btn primary" disabled={!can.gcp_install} onClick={() => void job.start("api/connect/gcp/install")}>Install the Google Cloud CLI</button>
          ) : (
            <a className="btn primary" href="https://cloud.google.com/sdk/docs/install" target="_blank" rel="noreferrer">Get the Google Cloud CLI</a>
          )}
          <button type="button" className="btn" onClick={() => void refresh()}>Check again</button>
        </div>
        {state.brew && <p className="muted small">Runs <code>brew install --cask gcloud-cli</code>. It takes a few minutes.</p>}
      </>
    );
  } else if (!signedIn) {
    signinBody = (
      <>
        {state.problem && <div className="callout fail">{state.problem}</div>}
        <p>Sign in with the Google account that should own the new resources.</p>
        <button type="button" className="btn primary" disabled={!can.gcp_signin} onClick={() => void job.start("api/connect/gcp/signin")}>Sign in with Google</button>
      </>
    );
  }

  // 2. Project
  let projectBody: ReactNode = null;
  if (signedIn && !projectDone) {
    const list = projects ?? [];
    projectBody = (
      <>
        {projects === null && !projErr && <p className="muted">Loading your projects…</p>}
        {list.length > 0 && (
          <Field label="Where the new resources go" hint="The projects your Google account can see.">
            <select value={list.some((p) => p.id === g.project_id) ? g.project_id : ""} onChange={(e) => chooseProject(e.target.value)}>
              <option value="" disabled>Choose a project</option>
              {list.map((p) => <option key={p.id} value={p.id}>{p.name && p.name !== p.id ? `${p.name} (${p.id})` : p.id}</option>)}
            </select>
          </Field>
        )}
        {(projErr || (projects !== null && list.length === 0)) && (
          <>
            {projErr ? <p className="note">Hermes could not list your projects: {projErr} Type the project ID instead.</p> : <p className="note">Your account cannot see any project yet.</p>}
            <div className="row-actions">
              <input className="grow" value={typed} onChange={(e) => setTyped(e.target.value.trim())} placeholder="my-sandbox-123456" spellCheck={false} aria-label="Project ID" />
              <button type="button" className="btn primary" disabled={!/^[a-z][a-z0-9-]{4,28}[a-z0-9]$/.test(typed)} onClick={() => chooseProject(typed)}>Use this project</button>
            </div>
          </>
        )}
        <p className="muted small">
          Need a new one? <a href="https://console.cloud.google.com/projectcreate" target="_blank" rel="noreferrer">Create a project</a> in the console, then <button type="button" className="linkish" onClick={() => void refresh()}>reload the list</button>.
        </p>
        <label className="inline-field">
          Region for new resources
          <select value={g.region} onChange={(e) => setGcp({ region: e.target.value })}>
            {[...new Set([g.region, ...gcpRegions])].map((r) => <option key={r} value={r}>{r}</option>)}
          </select>
        </label>
      </>
    );
  }

  // 3. Check
  let checkBody: ReactNode = null;
  let checkSummary: ReactNode = null;
  if (!projectDone) checkSummary = <span className="muted">After you choose a project.</span>;
  else if (busyKind === "gcp-apis" && job.job) checkBody = <JobSteps job={job.job} />;
  else if (checking) checkBody = <div className="waiting" role="status"><span className="pulse" aria-hidden="true" /><strong>Checking billing, APIs and your permissions…</strong></div>;
  else if (check && !check.ok) checkBody = <Problems steps={check.steps} />;
  else if (g.ok) {
    checkSummary = <>Billing, APIs and permissions are in place. {g.checked && <span className="muted">Checked {when(g.checked)}.</span>}</>;
    checkBody = check ? (
      <details>
        <summary>Show the checks</summary>
        <Checklist steps={check.steps} />
      </details>
    ) : null;
  } else if (!check) {
    checkBody = <button type="button" className="btn primary" onClick={() => void runCheck()}>Check this project</button>;
  }
  const fixButtons = projectDone && !checking && !busyKind && (fixes.billing || fixes.apis) && (
    <div className="row-actions">
      {fixes.billing && (
        <a className="btn" href={`https://console.cloud.google.com/billing/linkedaccount?project=${encodeURIComponent(g.project_id)}`} target="_blank" rel="noreferrer">Open billing</a>
      )}
      {fixes.apis && (
        <button type="button" className={`btn${fixes.apis === "required" ? " primary" : ""}`} disabled={!can.gcp_apis} onClick={() => void job.start("api/connect/gcp/apis", { project_id: g.project_id })}>
          Turn on the APIs Hermes uses
        </button>
      )}
      {fixes.billing && <button type="button" className="btn" onClick={() => void runCheck()}>Check again</button>}
    </div>
  );

  return (
    <CloudCard side="to" name="Google Cloud" ready={g.ok}>
      <Leg
        n={1}
        title="Sign in"
        state={signedIn && busyKind !== "gcp-signin" ? "done" : state?.problem ? "fail" : "current"}
        summary={signedIn && busyKind !== "gcp-signin" && <span className="mono">{state?.account || "Signed in"}</span>}
        action={signedIn && busyKind !== "gcp-signin" && <button type="button" className="btn small" onClick={() => void job.start("api/connect/gcp/signin")}>Switch account</button>}
      >
        {signedIn && busyKind !== "gcp-signin" ? null : signinBody}
        {job.job && (job.job.kind === "gcp-signin" || job.job.kind === "gcp-install") && <JobError job={job.job} error={null} />}
      </Leg>
      <Leg
        n={2}
        title="Project"
        state={!signedIn ? "locked" : projectDone ? "done" : "current"}
        summary={!signedIn ? <span className="muted">After you sign in.</span> : projectDone ? <><span className="mono">{g.project_id}</span> in {g.region}</> : null}
        action={projectDone && <button type="button" className="btn small" onClick={() => { setTyped(g.project_id); setChangingProject(true); }}>Change</button>}
      >
        {projectBody}
      </Leg>
      <Leg
        n={3}
        title="Ready to deploy"
        state={!projectDone ? "locked" : g.ok ? "done" : check && !check.ok ? "fail" : "current"}
        summary={checkSummary}
        action={projectDone && g.ok && !checking && <button type="button" className="btn small" onClick={() => void runCheck()}>Check again</button>}
      >
        {checkBody}
        {fixButtons}
        {fixes.apis && !busyKind && <p className="muted small">Turning an API on costs nothing by itself. Only resources created later cost money.</p>}
        {job.job?.kind === "gcp-apis" && <JobError job={job.job} error={null} />}
        {!job.job && <JobError job={null} error={job.error} />}
      </Leg>
    </CloudCard>
  );
}

const labels: Record<string, string> = { discover: "Read the AWS account", assess: "Compare with Google Cloud", plan: "Build the migration plan" };

function DiscoverPanel({ conn, setRegions, canRun, onDone }: { conn: Connection; setRegions: (r: string) => void; canRun: boolean; onDone: () => void }) {
  const a = conn.aws;
  const job = useLaneJob("discover", (j) => j.state === "done" && onDone());
  const ready = a.ok && !!a.role_arn;
  const running = job.running;
  return (
    <div className={`discover${ready ? "" : " waiting-for"}`}>
      <div className="discover-text">
        <h2>Discover and plan</h2>
        <p>
          {ready
            ? "Hermes reads the regions you list through the role, compares each resource with its closest Google Cloud service, and orders the move into phases with approval gates. It only reads."
            : "Connect AWS first. The plan needs only AWS; Google Cloud has to be ready when you deploy."}
        </p>
      </div>
      <div className="discover-form">
        <Field label="AWS regions to read" hint="Comma separated.">
          <input value={conn.regions} onChange={(e) => setRegions(e.target.value)} placeholder={a.region} spellCheck={false} disabled={!ready} />
        </Field>
        <button
          type="button"
          className="btn primary"
          disabled={!canRun || !ready || running}
          onClick={() =>
            void job.start("api/jobs/discover", {
              aws: { profile: a.profile, region: a.region, role_arn: a.role_arn, external_id: a.external_id },
              regions: parseRegions(conn.regions, a.region),
              target: "gcp",
            })
          }
        >
          {running ? "Working…" : job.job ? "Run again" : "Start discovery"}
        </button>
      </div>
      {job.error && <p className="error">{job.error}</p>}
      {job.job && job.job.steps.length > 0 && (
        <ol className="checklist compact discover-steps" aria-live="polite">
          {job.job.steps.map((s) => (
            <li key={s.name} className={s.status === "done" ? "pass" : s.status === "failed" ? "fail" : s.status === "warn" ? "warn" : "skip"}>
              <StatusIcon status={s.status} />
              <div><strong>{labels[s.name] ?? s.name}</strong>{s.detail && <p>{s.detail}</p>}</div>
            </li>
          ))}
        </ol>
      )}
      {job.job && (job.job.state === "failed" || job.job.state === "cancelled") && <div className="callout fail">{sentence(job.job.error ?? "Stopped.")}</div>}
      {job.job?.state === "done" && (
        <div className="callout pass">
          <strong>Done.</strong> <a href="#/overview">See the overview</a> or <a href="#/plan">open the plan</a>.
        </div>
      )}
    </div>
  );
}
