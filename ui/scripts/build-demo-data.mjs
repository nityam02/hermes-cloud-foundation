// Builds the synthetic demo documents in public/demo from the committed sample inventory.
// Needs a built CLI (make build). Run: npm run demo-data
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, writeFileSync, copyFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { parse } from "yaml";

const root = resolve(import.meta.dirname, "..", "..");
const hermes = join(root, "bin", "hermes");
const out = resolve(import.meta.dirname, "..", "public", "demo");
const tmp = mkdtempSync(join(tmpdir(), "hermes-demo-"));
const env = { ...process.env, AWS_CONFIG_FILE: "/dev/null", AWS_SHARED_CREDENTIALS_FILE: "/dev/null", AWS_EC2_METADATA_DISABLED: "true" };
const run = (...args) => execFileSync(hermes, args, { env, cwd: tmp, stdio: ["ignore", "ignore", "inherit"] });

copyFileSync(join(root, "examples", "migration", "sample-inventory.json"), join(out, "inventory.json"));
run("migrate", "assess", "--inventory", join(out, "inventory.json"), "--target", "gcp", "--out", join(tmp, "assessment.json"));
run("migrate", "plan", "--assessment", join(tmp, "assessment.json"), "--out", join(tmp, "plan.yaml"));
copyFileSync(join(tmp, "assessment.json"), join(out, "assessment.json"));
writeFileSync(join(out, "plan.json"), JSON.stringify(parse(readFileSync(join(tmp, "plan.yaml"), "utf8"))));
console.log("demo data written to", out);
