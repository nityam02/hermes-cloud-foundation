import type { Docs, Gap } from "../types";
import { Chip, Empty } from "../components";
import { shortId } from "../lib";

function GapList({ gaps }: { gaps: Gap[] }) {
  if (gaps.length === 0) return <p className="muted">No gaps recorded.</p>;
  return (
    <div className="scroll">
      <table>
        <thead><tr><th>Where</th><th>Call</th><th>Why</th><th>What it means</th></tr></thead>
        <tbody>
          {gaps.map((g, i) => (
            <tr key={i}>
              <td>{g.service} {g.region}</td>
              <td className="mono">{g.operation}</td>
              <td><Chip tone={g.reason === "access-denied" ? "mixed" : undefined}>{g.reason}</Chip></td>
              <td>{g.impact ?? g.message}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export function Gaps({ docs }: { docs: Docs }) {
  const gaps = docs.assessment?.gaps ?? docs.inventory?.gaps;
  if (!gaps && !docs.assessment) return <Empty what="assessment" command="hermes migrate assess --inventory inventory.json --target gcp --out assessment.json" />;
  const unsupported = docs.assessment?.unsupported ?? [];
  return (
    <>
      <section>
        <h1>What Hermes could not see or move</h1>
        <p className="lede">Gaps are calls the read-only role could not make. Anything not seen here is not in the plan, so each gap is migration risk until someone accepts it or widens access.</p>
        <GapList gaps={gaps ?? []} />
      </section>
      <section>
        <h2>Resources without a ready target</h2>
        <p className="muted">Hermes does not provision these yet, or they only partly map. They need a manual step in the plan.</p>
        {unsupported.length === 0 ? <p className="muted">None.</p> : (
          <div className="scroll">
            <table>
              <thead><tr><th>Resource</th><th>Type</th><th>Why</th><th>Suggested target</th></tr></thead>
              <tbody>
                {unsupported.map((u) => (
                  <tr key={u.source_id}>
                    <td>{u.source_name || shortId(u.source_id)}</td>
                    <td>{u.source_type.replace(/^AWS::/, "").replace(/::/g, " ")}</td>
                    <td>{u.reason}</td>
                    <td>{u.recommended_service ?? "none"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </>
  );
}
