import type { ReactNode } from "react";

export function Empty({ what, command }: { what: string; command: string }) {
  return (
    <div className="empty">
      <h2>No {what} loaded</h2>
      <p>
        Create it with <code>{command}</code>, then start the UI with the matching flag.
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
