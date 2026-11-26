import { Fragment, useMemo, useState } from "react";
import type { Docs } from "../types";
import { Chip, Empty, compatTone } from "../components";
import { filterResources, shortId, typeLabel, uniqueSorted } from "../lib";

export function Inventory({ docs }: { docs: Docs }) {
  const rs = docs.assessment?.resources;
  const [f, setF] = useState({ text: "", type: "", region: "", compatibility: "" });
  const [open, setOpen] = useState<string | null>(null);
  const shown = useMemo(() => (rs ? filterResources(rs, f) : []), [rs, f]);

  if (!rs) {
    return <Empty what="assessment" command="hermes migrate assess --inventory inventory.json --target gcp --out assessment.json" />;
  }
  const edges = docs.inventory?.relationships ?? [];

  return (
    <section>
      <h1>Inventory</h1>
      <p className="lede">Everything discovery found, and where each resource would land. {shown.length} of {rs.length} shown.</p>
      <div className="tools">
        <input type="search" aria-label="Search resources" placeholder="Search name, type or target" value={f.text} onChange={(e) => setF({ ...f, text: e.target.value })} />
        <select aria-label="Resource type" value={f.type} onChange={(e) => setF({ ...f, type: e.target.value })}>
          <option value="">All types</option>
          {uniqueSorted(rs.map((r) => r.type)).map((t) => <option key={t} value={t}>{typeLabel(t)}</option>)}
        </select>
        <select aria-label="Region" value={f.region} onChange={(e) => setF({ ...f, region: e.target.value })}>
          <option value="">All regions</option>
          {uniqueSorted(rs.map((r) => r.region)).map((t) => <option key={t} value={t}>{t}</option>)}
        </select>
        <select aria-label="Compatibility" value={f.compatibility} onChange={(e) => setF({ ...f, compatibility: e.target.value })}>
          <option value="">Any compatibility</option>
          {uniqueSorted(rs.map((r) => r.compatibility)).map((t) => <option key={t} value={t}>{t}</option>)}
        </select>
      </div>
      <div className="scroll">
        <table>
          <thead><tr><th>Resource</th><th>Type</th><th>Region</th><th>Lands on</th><th>Fit</th><th>Effort</th><th>Wave</th></tr></thead>
          <tbody>
            {shown.length === 0 && <tr><td colSpan={7} className="muted">No resource matches these filters.</td></tr>}
            {shown.map((r) => {
              const isOpen = open === r.id;
              const links = edges.filter((e) => e.from === r.id || e.to === r.id);
              return (
                <Fragment key={r.id}>
                  <tr>
                    <td><button className="row" aria-expanded={isOpen} onClick={() => setOpen(isOpen ? null : r.id)}>{r.name}</button></td>
                    <td>{typeLabel(r.type)}</td>
                    <td>{r.region}</td>
                    <td>{r.target_service}</td>
                    <td><Chip tone={compatTone(r.compatibility)}>{r.compatibility}</Chip></td>
                    <td>{r.complexity}</td>
                    <td>{r.wave}</td>
                  </tr>
                  {isOpen && (
                    <tr className="detail">
                      <td colSpan={7}>
                        <dl className="kv">
                          <dt>Id</dt><dd className="mono">{r.id}</dd>
                          <dt>Alternatives</dt><dd>{r.alternatives?.length ? r.alternatives.join(", ") : "none listed"}</dd>
                          <dt>Data to move</dt><dd>{r.data_migration ?? "unknown"}</dd>
                          <dt>Hermes can provision</dt><dd>{r.hermes_support ?? "unknown"}</dd>
                          <dt>Connected to</dt><dd>{links.length ? links.map((e) => `${e.type} ${shortId(e.from === r.id ? e.to : e.from)}`).join(", ") : "nothing recorded"}</dd>
                          {r.notes?.length ? (<><dt>Notes</dt><dd><ul style={{ margin: 0, paddingLeft: 18 }}>{r.notes.map((n, i) => <li key={i}>{n}</li>)}</ul></dd></>) : null}
                        </dl>
                      </td>
                    </tr>
                  )}
                </Fragment>
              );
            })}
          </tbody>
        </table>
      </div>
    </section>
  );
}
