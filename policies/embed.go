// Package policies embeds the Hermes plan policies (Rego, evaluated by Conftest against
// `tofu show -json` / `terraform show -json` output). The CLI extracts FS and runs
// `conftest test --policy <dir>/plan`. Only *.rego files under plan/ are embedded; tests and
// fixtures live in policies/tests and are not shipped.
package policies

import "embed"

// FS holds plan/*.rego.
//
//go:embed plan/*.rego
var FS embed.FS

// Dir is the directory inside FS that holds the policies.
const Dir = "plan"
