import { describe, expect, it } from "vitest";
import { providerName, filterResources, filterSteps, formatBytes, formatUSD, planCounts, scoreBand, shortId, typeLabel } from "./lib";
import type { AssessedResource, Plan, PlanStep } from "./types";

const res = (o: Partial<AssessedResource>): AssessedResource => ({
  id: "x", type: "AWS::S3::Bucket", name: "b", region: "eu-north-1", category: "storage", target_service: "Cloud Storage",
  compatibility: "compatible", complexity: "low", wave: 1, ...o,
});
const step = (o: Partial<PlanStep>): PlanStep => ({
  id: "s", title: "t", description: "", phase: "p", wave: 1, automation: "manual", gate: "none", source_impact: "none", reversible: true, ...o,
});

describe("lib", () => {
  it("shortens ids", () => {
    expect(shortId("arn:aws:ec2:eu-north-1:123:vpc/vpc-0abc")).toBe("vpc-0abc");
    expect(shortId("arn:aws:sns:eu-north-1:123:topic-name")).toBe("topic-name");
    expect(shortId("plain")).toBe("plain");
  });
  it("names providers", () => {
    expect(providerName("aws", "x")).toBe("AWS");
    expect(providerName("alibaba", "x")).toBe("Alibaba Cloud");
    expect(providerName(undefined, "Source")).toBe("Source");
  });
  it("labels types", () => expect(typeLabel("AWS::EC2::Subnet")).toBe("EC2 Subnet"));
  it("formats bytes and money", () => {
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes(1500)).toBe("1.5 KB");
    expect(formatBytes(240518168576)).toBe("241 GB");
    expect(formatUSD(12.03)).toBe("$12.03");
    expect(formatUSD(1234)).toBe("$1,234");
  });
  it("bands the score", () => {
    expect(scoreBand(80).tone).toBe("good");
    expect(scoreBand(50).tone).toBe("mixed");
    expect(scoreBand(10).tone).toBe("hard");
  });
  it("filters resources", () => {
    const rs = [res({ name: "logs", region: "eu-north-1" }), res({ name: "data", region: "us-east-1", compatibility: "partial" })];
    expect(filterResources(rs, { text: "", type: "", region: "us-east-1", compatibility: "" })).toHaveLength(1);
    expect(filterResources(rs, { text: "LOG", type: "", region: "", compatibility: "" })).toHaveLength(1);
    expect(filterResources(rs, { text: "", type: "", region: "", compatibility: "partial" })[0].name).toBe("data");
    expect(filterResources(rs, { text: "", type: "AWS::SQS::Queue", region: "", compatibility: "" })).toHaveLength(0);
  });
  it("filters steps and counts gates", () => {
    const steps = [step({ gate: "approval", source_impact: "modifies-source-config" }), step({ gate: "approval" }), step({})];
    expect(filterSteps(steps, { approvalOnly: true, sourceOnly: false })).toHaveLength(2);
    expect(filterSteps(steps, { approvalOnly: true, sourceOnly: true })).toHaveLength(1);
    const plan = { phases: [{ id: "a", order: 1, steps }] } as unknown as Plan;
    expect(planCounts(plan)).toEqual({ steps: 3, approvals: 2, source: 1 });
  });
});
