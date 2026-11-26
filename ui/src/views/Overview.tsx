import type { Docs } from "../types";
import { Chip, Empty } from "../components";
import { formatBytes, formatUSD, providerName, scoreBand, shortId, typeLabel } from "../lib";

const order = ["equivalent", "compatible", "partial", "re-architect", "unsupported"];
const tones: Record<string, "good" | "mixed" | "hard"> = { equivalent: "good", compatible: "good", partial: "mixed", "re-architect": "hard", unsupported: "hard" };

export function Overview({ docs }: { docs: Docs }) {
  const a = docs.assessment;
  if (!a) {
    return <Empty what="assessment" command="hermes migrate assess --inventory inventory.json --target gcp --out assessment.json" />;
  }
  const s = a.summary;
  const band = scoreBand(s.portability_score);
  const total = Math.max(1, s.resource_count);
  const byWave = new Map<number, string[]>();
  (a.dependencies?.waves ?? []).forEach((w) => byWave.set(w.index, w.resources));

  return (
    <>
      <section>
        <h1>
          {providerName(a.metadata?.source?.provider, "Source")} to {providerName(a.metadata?.target?.provider, "target")}
        </h1>
        <p className="lede">
          {s.resource_count} resources assessed. {s.blocker_count} blockers, {s.risk_count} risks, {s.gap_count} discovery gaps.
        </p>
        <div className="score">
          <div className="score-top">
            <span className="score-num">{Math.round(s.portability_score)}%</span>
            <Chip tone={band.tone}>{band.label}</Chip>
          </div>
          <div className="bar" role="img" aria-label="Resources by compatibility">
            {order.filter((k) => s.by_compatibility[k]).map((k) => (
              <span key={k} className={tones[k]} style={{ width: `${(100 * s.by_compatibility[k]) / total}%` }} title={`${k}: ${s.by_compatibility[k]}`} />
            ))}
          </div>
          <div className="legend">
            {order.filter((k) => s.by_compatibility[k]).map((k) => (
              <span key={k}>
                <i className="dot" style={{ background: `var(--${tones[k] === "good" ? "good" : tones[k] === "mixed" ? "risk" : "block"})` }} />
                {s.by_compatibility[k]} {k}
              </span>
            ))}
          </div>
          <details>
            <summary>How the score is calculated</summary>
            <p className="muted">{s.portability_method}</p>
          </details>
        </div>
        <div className="facts">
          <div className="fact"><b>{s.wave_count}</b><span>migration waves</span></div>
          <div className="fact"><b>{formatBytes(s.data_volume?.required_bytes ?? 0)}</b><span>data that must move</span></div>
          {s.transfer_cost && (
            <div className="fact">
              <b>{formatUSD(s.transfer_cost.low_usd)} to {formatUSD(s.transfer_cost.high_usd)}</b>
              <span>indicative transfer cost, not a quote</span>
            </div>
          )}
        </div>
      </section>

      <section>
        <h2>Blockers and risks</h2>
        {a.blockers.length === 0 && <p className="muted">None found.</p>}
        {a.blockers.map((b, i) => (
          <div key={`${b.code}-${i}`} className={`issue ${b.severity === "blocker" ? "blocker" : ""}`}>
            <strong>{b.message}</strong> <Chip tone={b.severity === "blocker" ? "hard" : "mixed"}>{b.severity}</Chip> <span className="muted mono">{b.code}</span>
            <p>{b.recommendation}</p>
            {b.resource_ids.length > 0 && <p className="muted mono">{b.resource_ids.slice(0, 4).map(shortId).join(", ")}{b.resource_ids.length > 4 ? ` and ${b.resource_ids.length - 4} more` : ""}</p>}
          </div>
        ))}
      </section>

      {byWave.size > 0 && (
        <section>
          <h2>Migration order</h2>
          <p className="muted">A resource moves after everything it depends on. Resources in the same wave can move together.</p>
          <div className="scroll">
            <table>
              <thead><tr><th>Wave</th><th>Resources</th><th>What is in it</th></tr></thead>
              <tbody>
                {[...byWave.entries()].sort((x, y) => x[0] - y[0]).map(([w, ids]) => {
                  const types = new Map<string, number>();
                  ids.forEach((id) => {
                    const r = a.resources.find((x) => x.id === id);
                    const t = typeLabel(r?.type ?? "other");
                    types.set(t, (types.get(t) ?? 0) + 1);
                  });
                  return (
                    <tr key={w}>
                      <td>{w}</td>
                      <td>{ids.length}</td>
                      <td>{[...types.entries()].map(([t, n]) => `${n} ${t}`).join(", ")}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </section>
      )}
      {a.metadata?.disclaimer && <p className="muted" style={{ marginTop: 28 }}>{a.metadata.disclaimer}</p>}
    </>
  );
}
