import { useEffect, useRef, useState } from "react";
import { api, type CheckResult, type Job, type Status } from "../api";
import { Checklist, CopyButton, Field, StatusIcon } from "../components";
import { parseRegions, saveSession, type Session } from "../session";

type Props = {
  session: Session;
  setSession: (s: Session) => void;
  status: Status;
  onDiscovered: () => void;
};

const stepNames = ["Connect AWS", "Connect Google Cloud", "Discover and plan"];

export function Connect({ session, setSession, status, onDiscovered }: Props) {
  const [step, setStep] = useState(session.awsOk ? (session.gcpOk ? 2 : 1) : 0);
  const update = (patch: Partial<Session>) => {
    const next = { ...session, ...patch };
    saveSession(next);
    setSession(next);
  };
  const reached = [true, session.awsOk, session.awsOk && session.gcpOk];

  return (
    <section>
      <h1>Connect your clouds</h1>
      <p className="lede">
        Hermes reads your AWS account through a read-only role, checks your Google Cloud project, then builds a migration plan. Nothing is created or changed in either cloud.
      </p>
      <ol className="stepper" aria-label="Progress">
        {stepNames.map((n, i) => {
          const done = (i === 0 && session.awsOk) || (i === 1 && session.gcpOk);
          return (
            <li key={n} className={`${i === step ? "current" : ""} ${done ? "done" : ""}`}>
              <button type="button" disabled={!reached[i]} aria-current={i === step ? "step" : undefined} onClick={() => setStep(i)}>
                <span className="stop-dot" aria-hidden="true">{done ? "✓" : i + 1}</span>
                {n}
              </button>
            </li>
          );
        })}
      </ol>
      {step === 0 && <AwsStep session={session} update={update} canCheck={status.can.connect_aws} next={() => setStep(1)} />}
      {step === 1 && <GcpStep session={session} update={update} canCheck={status.can.connect_gcp} back={() => setStep(0)} next={() => setStep(2)} />}
      {step === 2 && <RunStep session={session} update={update} canRun={status.can.discover} back={() => setStep(1)} onDone={onDiscovered} />}
    </section>
  );
}

function useAction<T>() {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const run = async (fn: () => Promise<T>): Promise<T | undefined> => {
    setBusy(true);
    setError(null);
    try {
      return await fn();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      return undefined;
    } finally {
      setBusy(false);
    }
  };
  return { busy, error, run };
}

type Identity = { ok: boolean; arn?: string; account?: string; root?: boolean; error?: { detail: string; fix?: string } };
type Template = { external_id: string; template: string; command: string; console_url: string; role_name: string };

function AwsStep({ session, update, canCheck, next }: { session: Session; update: (p: Partial<Session>) => void; canCheck: boolean; next: () => void }) {
  const [profiles, setProfiles] = useState<string[]>([]);
  const [identity, setIdentity] = useState<Identity | null>(null);
  const [tpl, setTpl] = useState<Template | null>(null);
  const [result, setResult] = useState<CheckResult | null>(null);
  const who = useAction<void>();
  const check = useAction<void>();

  useEffect(() => {
    void api.get<{ profiles: string[] }>("api/connect/aws/profiles").then((r) => setProfiles(r.profiles ?? []), () => undefined);
  }, []);

  const findMe = () =>
    who.run(async () => {
      const r = await api.post<Identity>("api/connect/aws/identity", { profile: session.awsProfile, region: session.awsRegion });
      setIdentity(r);
      setResult(null);
      if (r.ok && r.arn && !r.root) {
        update({ awsArn: r.arn });
        const t = await api.post<Template>("api/connect/aws/template", { principal: r.arn, region: session.awsRegion, externalId: session.externalId });
        setTpl(t);
        update({ awsArn: r.arn, externalId: t.external_id });
      }
    });

  const download = () => {
    if (!tpl) return;
    const url = URL.createObjectURL(new Blob([tpl.template], { type: "application/json" }));
    const a = document.createElement("a");
    a.href = url;
    a.download = "hermes-discovery-role.json";
    a.click();
    URL.revokeObjectURL(url);
  };

  const runCheck = () =>
    check.run(async () => {
      const r = await api.post<CheckResult>("api/connect/aws/check", {
        profile: session.awsProfile,
        region: session.awsRegion,
        role_arn: session.roleArn.trim(),
        external_id: session.externalId,
      });
      setResult(r);
      update({ awsOk: r.ok });
    });

  const signedIn = identity?.ok && !identity.root;

  return (
    <div className="panel">
      <h2>1. Who are you in AWS?</h2>
      <p>Pick the sign-in Hermes should use on this machine. Hermes never asks for access keys.</p>
      <div className="row">
        <Field label="AWS profile" hint={profiles.length ? "From your AWS config." : "No profiles found: the default sign-in is used."}>
          <select value={session.awsProfile} onChange={(e) => update({ awsProfile: e.target.value })}>
            <option value="">Default sign-in</option>
            {profiles.map((p) => <option key={p} value={p}>{p}</option>)}
          </select>
        </Field>
        <Field label="Region" hint="Where to run the quick read test.">
          <input value={session.awsRegion} onChange={(e) => update({ awsRegion: e.target.value.trim() })} spellCheck={false} />
        </Field>
      </div>
      <button className="btn primary" disabled={!canCheck || who.busy} onClick={() => void findMe()}>{who.busy ? "Checking…" : "Check my sign-in"}</button>
      {who.error && <p className="error">{who.error}</p>}
      {identity && !identity.ok && (
        <div className="callout fail" role="alert">
          <strong>{identity.error?.detail ?? "Could not read your AWS sign-in."}</strong>
          {identity.error?.fix && <p>{identity.error.fix}</p>}
        </div>
      )}
      {identity?.ok && identity.root && (
        <div className="callout fail" role="alert">
          <strong>You are signed in as the account's root user.</strong>
          <p>AWS never lets the root user assume a role, so Hermes cannot use it. Sign in as an IAM user, an IAM role or an SSO identity, choose that profile, and check again.</p>
        </div>
      )}
      {signedIn && <div className="callout pass"><StatusIcon status="pass" /> Signed in as <span className="mono">{identity.arn}</span> (account {identity.account}).</div>}

      {signedIn && tpl && (
        <>
          <h2>2. Create the read-only role</h2>
          <p>This role can only list and describe resources. It cannot read secrets, files or data, and it trusts only your sign-in above, with the external ID Hermes made for you.</p>
          <ol className="howto">
            <li>
              <button className="btn" onClick={download}>Download the template</button>
            </li>
            <li>
              Open <a href={tpl.console_url} target="_blank" rel="noreferrer">CloudFormation, create stack</a>, choose <b>Upload a template file</b> and pick the file.
            </li>
            <li>
              Enter these two values, tick the box that acknowledges IAM resources, and create the stack:
              <dl className="kv">
                <dt>ExternalId</dt><dd><span className="mono">{tpl.external_id}</span> <CopyButton text={tpl.external_id} /></dd>
                <dt>TrustedPrincipalArn</dt><dd><span className="mono">{session.awsArn}</span> <CopyButton text={session.awsArn} /></dd>
              </dl>
            </li>
            <li>When the stack is complete, open its <b>Outputs</b> tab and copy <b>RoleArn</b>.</li>
          </ol>
          <details>
            <summary>Prefer the command line?</summary>
            <p className="muted">Save the template as <code>hermes-discovery-role.json</code>, then run:</p>
            <pre className="code">{tpl.command}</pre>
            <CopyButton text={tpl.command} label="Copy command" />
          </details>

          <h2>3. Check the connection</h2>
          <Field label="Role ARN" hint="The RoleArn output of the stack.">
            <input value={session.roleArn} onChange={(e) => update({ roleArn: e.target.value, awsOk: false })} placeholder="arn:aws:iam::123456789012:role/HermesDiscoveryReadOnly" spellCheck={false} />
          </Field>
          <button className="btn primary" disabled={!canCheck || check.busy || !session.roleArn} onClick={() => void runCheck()}>{check.busy ? "Checking…" : "Check connection"}</button>
          {check.error && <p className="error">{check.error}</p>}
          {result && (
            <>
              <h3 className="result-title">{result.summary}</h3>
              <Checklist steps={result.steps} />
            </>
          )}
        </>
      )}
      {session.awsOk && (
        <div className="actions">
          <button className="btn primary" onClick={next}>Continue to Google Cloud</button>
        </div>
      )}
    </div>
  );
}

function GcpStep({ session, update, canCheck, back, next }: { session: Session; update: (p: Partial<Session>) => void; canCheck: boolean; back: () => void; next: () => void }) {
  const [result, setResult] = useState<CheckResult | null>(null);
  const check = useAction<void>();
  const run = () =>
    check.run(async () => {
      const r = await api.post<CheckResult>("api/connect/gcp/check", { project_id: session.gcpProject.trim(), region: session.gcpRegion.trim() });
      setResult(r);
      update({ gcpOk: r.ok });
    });
  return (
    <div className="panel">
      <h2>Which Google Cloud project?</h2>
      <p>Hermes uses the sign-in you already have on this machine. If you have not signed in yet, run this in a terminal first:</p>
      <pre className="code">gcloud auth application-default login</pre>
      <CopyButton text="gcloud auth application-default login" label="Copy command" />
      <div className="row" style={{ marginTop: 16 }}>
        <Field label="Project ID" hint="The ID, not the display name. It is in the console project picker.">
          <input value={session.gcpProject} onChange={(e) => update({ gcpProject: e.target.value, gcpOk: false })} placeholder="my-sandbox-123456" spellCheck={false} />
        </Field>
        <Field label="Region" hint="Where new resources should live.">
          <input value={session.gcpRegion} onChange={(e) => update({ gcpRegion: e.target.value, gcpOk: false })} spellCheck={false} />
        </Field>
      </div>
      <button className="btn primary" disabled={!canCheck || check.busy || !session.gcpProject} onClick={() => void run()}>{check.busy ? "Checking…" : "Check Google Cloud"}</button>
      {check.error && <p className="error">{check.error}</p>}
      {result && (
        <>
          <h3 className="result-title">{result.summary}</h3>
          <Checklist steps={result.steps} />
        </>
      )}
      <div className="actions">
        <button className="btn" onClick={back}>Back</button>
        {session.gcpOk && <button className="btn primary" onClick={next}>Continue</button>}
      </div>
    </div>
  );
}

function RunStep({ session, update, canRun, back, onDone }: { session: Session; update: (p: Partial<Session>) => void; canRun: boolean; back: () => void; onDone: () => void }) {
  const [job, setJob] = useState<Job | null>(null);
  const start = useAction<void>();
  const timer = useRef<number | undefined>(undefined);

  useEffect(() => () => window.clearInterval(timer.current), []);

  const poll = (id: string) => {
    window.clearInterval(timer.current);
    timer.current = window.setInterval(() => {
      void api.get<Job>(`api/jobs/${id}`).then((j) => {
        setJob(j);
        if (j.state !== "running") {
          window.clearInterval(timer.current);
          if (j.state === "done") onDone();
        }
      }, () => undefined);
    }, 1000);
  };

  const run = () =>
    start.run(async () => {
      const { id } = await api.post<{ id: string }>("api/jobs/discover", {
        aws: { profile: session.awsProfile, region: session.awsRegion, role_arn: session.roleArn.trim(), external_id: session.externalId },
        regions: parseRegions(session.regions, session.awsRegion),
        target: "gcp",
      });
      setJob({ id, kind: "discover", state: "running", steps: [] });
      poll(id);
    });

  const running = job?.state === "running";
  return (
    <div className="panel">
      <h2>Discover your AWS account and build the plan</h2>
      <p>Hermes reads the regions you list through the read-only role, compares each resource with its closest Google Cloud service, and orders the move into phases with approval gates. This only reads.</p>
      <Field label="AWS regions to read" hint="Comma separated. Leave empty for the region you picked earlier.">
        <input value={session.regions} onChange={(e) => update({ regions: e.target.value })} placeholder={session.awsRegion} spellCheck={false} />
      </Field>
      <button className="btn primary" disabled={!canRun || start.busy || running} onClick={() => void run()}>{running ? "Working…" : job ? "Run again" : "Start discovery"}</button>
      {start.error && <p className="error">{start.error}</p>}
      {job && (
        <ol className="checklist" aria-live="polite" style={{ marginTop: 20 }}>
          {job.steps.map((s) => (
            <li key={s.name} className={s.status === "done" ? "pass" : s.status === "failed" ? "fail" : s.status === "warn" ? "warn" : "skip"}>
              <StatusIcon status={s.status} />
              <div><strong>{labels[s.name] ?? s.name}</strong>{s.detail && <p>{s.detail}</p>}</div>
            </li>
          ))}
          {job.state === "failed" && (
            <li className="fail"><StatusIcon status="failed" /><div><strong>Stopped</strong><p>{job.error}</p></div></li>
          )}
        </ol>
      )}
      {job?.state === "done" && (
        <div className="callout pass">
          <strong>Done.</strong> The results are on the other pages. <a href="#/overview">See the overview</a> or <a href="#/plan">open the plan</a>.
        </div>
      )}
      <div className="actions"><button className="btn" onClick={back}>Back</button></div>
    </div>
  );
}

const labels: Record<string, string> = { discover: "Read the AWS account", assess: "Compare with Google Cloud", plan: "Build the migration plan" };
