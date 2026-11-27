import { renderToString } from "react-dom/server";
import { describe, expect, it } from "vitest";
import assessment from "../../public/demo/assessment.json";
import inventory from "../../public/demo/inventory.json";
import plan from "../../public/demo/plan.json";
import { normalizeDocs } from "../lib";
import type { Docs } from "../types";
import { ConnectBoard } from "./Connect";
import { sentence, type CheckResult, type Identity } from "../api";
import { gcpFixes, pickProfile, profileState, roleState, shortArn } from "../connectLogic";
import { Gaps } from "./Gaps";
import { Inventory } from "./Inventory";
import { Overview } from "./Overview";
import { PlanView } from "./Plan";
import { withDefaults } from "../session";

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

describe("connect page", () => {
  const can = { connect_aws: true, connect_gcp: true, discover: true, aws_signin: true, aws_role: true, gcp_signin: true, gcp_install: true, gcp_apis: true };
  const status = { inventory: false, assessment: false, plan: false, version: "t", can };
  const board = (conn = withDefaults(null)) =>
    renderToString(<ConnectBoard conn={conn} setAws={() => undefined} setGcp={() => undefined} setRegions={() => undefined} status={status} onDiscovered={() => undefined} />).replace(/<!-- -->/g, "");

  it("shows both clouds with three steps each, AWS first, and discovery waiting for AWS", () => {
    const html = board();
    expect(html).toContain("Connect your clouds");
    expect(html).toContain('aria-label="From AWS"');
    expect(html).toContain('aria-label="To Google Cloud"');
    expect(html.match(/class="leg /g)?.length).toBe(6);
    expect(html).toContain("Read-only role");
    expect(html).toContain("Ready to deploy");
    expect(html).toContain("Connect AWS first");
    expect(html).toMatch(/<button type="button" class="btn primary" disabled="">Start discovery/);
  });
  it("never offers a field for passwords, access keys or key files", () => {
    const html = board().toLowerCase();
    for (const bad of ["secret access key", "access key id", "json key", "private key", "password"]) {
      expect(html.replace("never sees your password", "")).not.toContain(bad);
    }
  });
  it("shows what was remembered and lets discovery start once AWS is connected", () => {
    const conn = withDefaults({ aws: { ...withDefaults(null).aws, ok: true, role_arn: "arn:aws:iam::123456789012:role/HermesDiscoveryReadOnly", identity: "arn:aws:iam::123456789012:user/op" } });
    const html = board(conn);
    expect(html).toContain("Connected");
    expect(html).toMatch(/<button type="button" class="btn primary">Start discovery/);
  });
});

describe("connect decisions", () => {
  const ok = (name: string, arn: string): Identity => ({ name, ok: true, arn, account: "123456789012", kind: "login" });
  it("starts with the remembered profile, else the first that works and is not root", () => {
    const scan: Identity[] = [{ name: "default", ok: false, kind: "other" }, { ...ok("demo", "arn:aws:iam::1:root"), root: true }, ok("op", "arn:aws:iam::1:user/op")];
    expect(pickProfile(scan, "")).toBe("op");
    expect(pickProfile(scan, "default")).toBe("default");
    expect(pickProfile(scan, "gone")).toBe("op");
    expect(pickProfile([{ name: "default", ok: false }], "")).toBe("");
  });
  it("describes sign-ins in plain words", () => {
    expect(profileState({ ...ok("a", "arn:aws:iam::1:root"), root: true }).tone).toBe("hard");
    expect(profileState(ok("a", "arn:aws:iam::123456789012:user/ops/alice")).text).toBe("Signed in as user/ops/alice");
    expect(shortArn("arn:aws:sts::123456789012:assumed-role/Admin/alice")).toBe("role/Admin/alice");
  });
  it("knows when the role is ready, missing, busy, broken or trusts someone else", () => {
    const me = "arn:aws:iam::1:user/op";
    const stack = { status: "CREATE_COMPLETE", role_arn: "arn:aws:iam::1:role/HermesDiscoveryReadOnly", trusted_principal: me };
    expect(roleState(null, me)).toBe("loading");
    expect(roleState({ found: false }, me)).toBe("none");
    expect(roleState({ found: false, error: "denied" }, me)).toBe("error");
    expect(roleState({ found: true, stack }, me)).toBe("ready");
    expect(roleState({ found: true, stack: { ...stack, status: "UPDATE_IN_PROGRESS" } }, me)).toBe("busy");
    expect(roleState({ found: true, stack: { ...stack, status: "ROLLBACK_COMPLETE" } }, me)).toBe("broken");
    expect(roleState({ found: true, stack: { ...stack, trusted_principal: "arn:aws:iam::1:user/other" } }, me)).toBe("other-principal");
  });
  it("turns Google check results into the right buttons", () => {
    const r = (steps: CheckResult["steps"]): CheckResult => ({ ok: false, summary: "", steps });
    expect(gcpFixes(r([{ id: "billing", title: "", status: "fail" }])).billing).toBe(true);
    expect(gcpFixes(r([{ id: "apis", title: "", status: "fail" }])).apis).toBe("required");
    expect(gcpFixes(r([{ id: "apis", title: "", status: "pass", detail: "3 more are needed when you deploy" }])).apis).toBe("recommended");
    expect(gcpFixes(r([{ id: "apis", title: "", status: "pass", detail: "Every API Hermes uses is already enabled." }])).apis).toBe(null);
  });
  it("shows Go errors as sentences", () => {
    expect(sentence("the AWS CLI is not installed")).toBe("The AWS CLI is not installed.");
    expect(sentence("Done.")).toBe("Done.");
  });
});
