// Package infra embeds the OpenTofu/Terraform modules shipped with the hermes binary:
// the provider contract, capability modules, provider compositions and state-backend
// bootstrap modules. The renderer extracts them to .hermes/modules/<cli-version>/.
//
// The default //go:embed pattern semantics exclude files and directories whose names
// begin with "." or "_", so .terraform/ directories and .terraform.lock.hcl files in a
// developer's checkout are never embedded (verified by embed_test.go).
package infra

import "embed"

// FS contains contract/, modules/, providers/ and bootstrap/.
//
//go:embed contract modules providers bootstrap
var FS embed.FS
