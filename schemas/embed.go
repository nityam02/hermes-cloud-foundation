// Package schemas embeds the JSON Schemas of every Hermes document kind
// (see docs/architecture/data-contracts.md). Only *.json files one directory deep are
// embedded (schemas/<kind>/<file>.json); dot-files are never embedded.
package schemas

import "embed"

// FS holds schemas/<kind>/*.json, e.g. "platform/v1alpha1.schema.json".
//
//go:embed */*.json
var FS embed.FS

// PlatformV1Alpha1 is the path of the Platform schema inside FS.
const PlatformV1Alpha1 = "platform/v1alpha1.schema.json"
