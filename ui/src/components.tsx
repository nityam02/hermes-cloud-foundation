import { useState, type ReactNode } from "react";
import type { CheckStep } from "./api";

export function Empty({ what, command }: { what: string; command: string }) {
  return (
    <div className="empty">
      <h2>No {what} loaded</h2>
      <p>
        Run discovery from the <a href="#/connect">Connect</a> page, or create it with <code>{command}</code> and start the UI with the matching flag.
      </p>
    </div>
  );
}

export function Chip({ tone, children }: { tone?: "good" | "mixed" | "hard" | "gate"; children: ReactNode }) {
  return <span className={`chip${tone ? ` ${tone}` : ""}`}>{children}</span>;
}

/** Tone for a compatibility value: how much work the move needs. */
export function compatTone(c: string): "good" | "mixed" | "hard" {
  if (c === "equivalent" || c === "compatible") return "good";
  if (c === "partial") return "mixed";
  return "hard";
}


const icons: Record<string, { mark: string; label: string }> = {
  pass: { mark: "✓", label: "Passed" },
  fail: { mark: "✕", label: "Failed" },
  warn: { mark: "!", label: "Needs attention" },
  skip: { mark: "–", label: "Skipped" },
  running: { mark: "…", label: "Running" },
  done: { mark: "✓", label: "Done" },
  failed: { mark: "✕", label: "Failed" },
};

export function StatusIcon({ status }: { status: string }) {
  const i = icons[status] ?? icons.skip;
  return (
    <span className={`status-icon ${status}`} role="img" aria-label={i.label}>
      {i.mark}
    </span>
  );
}

/** A connection test: one row per check, with what was found and how to fix it. */
export function Checklist({ steps }: { steps: CheckStep[] }) {
  return (
    <ol className="checklist" aria-live="polite">
      {steps.map((s) => (
        <li key={s.id} className={s.status}>
          <StatusIcon status={s.status} />
          <div>
            <strong>{s.title}</strong>
            {s.detail && <p>{s.detail}</p>}
            {s.fix && s.status !== "pass" && <p className="fix"><b>How to fix:</b> {s.fix}</p>}
          </div>
        </li>
      ))}
    </ol>
  );
}

export function CopyButton({ text, label = "Copy" }: { text: string; label?: string }) {
  const [done, setDone] = useState(false);
  return (
    <button
      type="button"
      className="btn small"
      onClick={() => {
        void navigator.clipboard?.writeText(text).then(
          () => {
            setDone(true);
            window.setTimeout(() => setDone(false), 1500);
          },
          () => undefined,
        );
      }}
    >
      {done ? "Copied" : label}
    </button>
  );
}

export function Field({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <label className="field">
      <span className="field-label">{label}</span>
      {children}
      {hint && <span className="field-hint">{hint}</span>}
    </label>
  );
}
