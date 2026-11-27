import { renderToString } from "react-dom/server";
import { describe, expect, it } from "vitest";
import assessment from "../../public/demo/assessment.json";
import inventory from "../../public/demo/inventory.json";
import plan from "../../public/demo/plan.json";
import { normalizeDocs } from "../lib";
import type { Docs } from "../types";
import { Connect } from "./Connect";
import { Gaps } from "./Gaps";
import { Inventory } from "./Inventory";
import { Overview } from "./Overview";
import { PlanView } from "./Plan";
import { defaults } from "../session";

const full = normalizeDocs(structuredClone({ mode: "demo", inventory, assessment, plan }) as unknown as Docs);
const empty: Docs = { mode: "empty" };

describe("normalizeDocs", () => {
  it("turns null lists from Go into empty arrays", () => {
    const d = normalizeDocs({
      mode: "live",
      assessment: { summary: {}, resources: null, blockers: [{ resource_ids: null }], gaps: null } as never,
      plan: { phases: [{ id: "a", order: 1, steps: null }] } as never,
    });
    expect(d.assessment?.resources).toEqual([]);
    expect(d.assessment?.blockers[0].resource_ids).toEqual([]);
    expect(d.plan?.phases[0].steps).toEqual([]);
  });
});

describe("views render the documents Hermes writes", () => {
  it("overview shows the route and the score", () => {
    const html = renderToString(<Overview docs={full} />);
    expect(html.replace(/<!-- -->/g, "")).toContain("AWS to GCP");
    expect(html).toContain("Blockers and risks");
  });
  it("inventory lists resources", () => {
    expect(renderToString(<Inventory docs={full} />)).toContain("Lands on");
  });
  it("plan shows phases and gates", () => {
    const html = renderToString(<PlanView docs={full} />);
    expect(html).toContain("Foundation");
    expect(html).toContain("needs approval");
  });
  it("gaps render", () => {
    expect(renderToString(<Gaps docs={full} />)).toContain("could not see");
  });
  it("every view shows an empty state with the command, never a blank page", () => {
    for (const V of [Overview, Inventory, PlanView]) {
      expect(renderToString(<V docs={empty} />)).toContain("hermes ");
    }
    expect(renderToString(<Gaps docs={empty} />)).toContain("hermes ");
  });
});

describe("connect flow", () => {
  const status = { inventory: false, assessment: false, plan: false, version: "t", can: { connect_aws: true, connect_gcp: true, discover: true } };
  it("starts at AWS with the sign-in check and no way to skip ahead", () => {
    const html = renderToString(<Connect session={{ ...defaults }} setSession={() => undefined} status={status} onDiscovered={() => undefined} />);
    expect(html).toContain("Connect your clouds");
    expect(html).toContain("Who are you in AWS?");
    expect(html).toContain("Check my sign-in");
    // later steps are locked until the earlier ones pass
    expect(html).toMatch(/<button type="button" disabled=""[^>]*><span class="stop-dot"[^>]*>2<\/span>Connect Google Cloud/);
    expect(html).not.toContain("Check Google Cloud");
  });
  it("never offers a field for access keys or key files", () => {
    const html = renderToString(<Connect session={{ ...defaults }} setSession={() => undefined} status={status} onDiscovered={() => undefined} />).toLowerCase();
    for (const bad of ["secret access key", "access key id", "json key", "private key", "password"]) {
      expect(html).not.toContain(bad);
    }
  });
  it("jumps to the Google step once AWS is connected", () => {
    const html = renderToString(<Connect session={{ ...defaults, awsOk: true }} setSession={() => undefined} status={status} onDiscovered={() => undefined} />);
    expect(html).toContain("Which Google Cloud project?");
    expect(html).toContain("gcloud auth application-default login");
  });
});
