import { useState } from "react";
import type { Docs, PlanStep } from "../types";
import { Chip, Empty } from "../components";
import { filterSteps, phaseTitle, planCounts, shortId, touchesSource } from "../lib";

function Step({ s }: { s: PlanStep }) {
  const [open, setOpen] = useState(false);
  return (
    <li className="step">
      <button aria-expanded={open} onClick={() => setOpen(!open)}>
        <span className="step-title">
          <i className={`gate-mark ${s.gate}`} aria-hidden="true" />
          {s.title}
        </span>
        <span className="step-tags">
          {s.gate === "approval" && <Chip tone="gate">needs approval</Chip>}
          {s.gate === "validation" && <Chip tone="gate">validation</Chip>}
          {touchesSource(s) && <Chip tone="mixed">changes source</Chip>}
          {!s.reversible && <Chip tone="hard">not reversible</Chip>}
          <Chip>{s.automation === "hermes" ? "Hermes" : s.automation === "manual" ? "manual" : s.automation}</Chip>
        </span>
      </button>
      {open && (
        <div className="step-body">
          <p style={{ marginTop: 10 }}>{s.description}</p>
          {s.validation?.length ? (<><h3>Check before moving on</h3><ul>{s.validation.map((v, i) => <li key={i}>{v}</li>)}</ul></>) : null}
          {s.risks?.length ? (<><h3>Risks</h3><ul>{s.risks.map((v, i) => <li key={i}>{v}</li>)}</ul></>) : null}
          <h3>If it goes wrong</h3>
          <p>{s.rollback ?? "No rollback is recorded for this step."}</p>
          {s.resource_refs?.length ? (<><h3>Resources involved</h3><p className="mono">{s.resource_refs.slice(0, 6).map(shortId).join(", ")}{s.resource_refs.length > 6 ? ` and ${s.resource_refs.length - 6} more` : ""}</p></>) : null}
        </div>
      )}
    </li>
  );
}

export function PlanView({ docs }: { docs: Docs }) {
  const p = docs.plan;
  const [approvalOnly, setApprovalOnly] = useState(false);
  const [sourceOnly, setSourceOnly] = useState(false);
  if (!p) return <Empty what="migration plan" command="hermes migrate plan --assessment assessment.json --out plan.yaml" />;
  const c = planCounts(p);
  const f = { approvalOnly, sourceOnly };

  return (
    <section>
      <h1>Migration plan</h1>
      <p className="lede">
        {c.steps} steps in {p.phases.length} phases. {c.approvals} need a person to approve them and {c.source} change the source account. Nothing here runs by itself: this page only shows the plan.
      </p>
      <p className="muted">Plan id <span className="mono">{p.metadata.plan_id.slice(0, 12)}</span>. Hermes checked it against the plan body when it loaded the file.</p>
      <div className="tools">
        <label className="check"><input type="checkbox" checked={approvalOnly} onChange={(e) => setApprovalOnly(e.target.checked)} /> Only steps that need approval</label>
        <label className="check"><input type="checkbox" checked={sourceOnly} onChange={(e) => setSourceOnly(e.target.checked)} /> Only steps that change the source</label>
      </div>
      <div className="route">
        {[...p.phases].sort((a, b) => a.order - b.order).map((ph) => {
          const steps = filterSteps(ph.steps, f);
          return (
            <div className="stop" key={ph.id}>
              <h2>{phaseTitle(ph.id)}</h2>
              <div className="stop-meta">{ph.steps.length} steps, {ph.steps.filter((s) => s.gate === "approval").length} approvals</div>
              {steps.length === 0 ? <p className="muted">No steps match these filters.</p> : <ul className="steps">{steps.map((s) => <Step key={s.id} s={s} />)}</ul>}
            </div>
          );
        })}
      </div>
      {p.notes?.length ? (<><h2>Notes</h2><ul>{p.notes.map((n, i) => <li key={i}>{n}</li>)}</ul></>) : null}
    </section>
  );
}
