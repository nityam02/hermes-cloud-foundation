// Package migrationdata embeds the maintained migration data files: the ServiceCatalog
// (mappings/), the MigrationWorkflow templates (workflows/) and the PricingAssumptions
// (pricing/). The migration engine uses them as defaults; every command accepts a file
// override.
package migrationdata

import "embed"

// FS holds mappings/catalog.yaml, workflows/*.yaml and pricing/assumptions.yaml.
//
//go:embed mappings workflows pricing
var FS embed.FS
