package providers

// Exact engine provider pins written into every rendered root module (required_providers).
// Modules use `~>` constraints; the root pins exact versions so a plan is reproducible.
// Source of the versions: registry.opentofu.org, latest stable releases on 2026-11-18.
// Keep these aligned with the versions the infra modules were tested with.
var (
	PinAWS        = TerraformProvider{LocalName: "aws", Source: "hashicorp/aws", Version: "6.73.0"}
	PinGoogle     = TerraformProvider{LocalName: "google", Source: "hashicorp/google", Version: "8.12.0"}
	PinGoogleBeta = TerraformProvider{LocalName: "google-beta", Source: "hashicorp/google-beta", Version: "8.12.0"}
	PinAlicloud   = TerraformProvider{LocalName: "alicloud", Source: "aliyun/alicloud", Version: "2.2.0"}
	PinRandom     = TerraformProvider{LocalName: "random", Source: "hashicorp/random", Version: "3.9.1"}
)

// SharedProviders are required by every provider composition in addition to the cloud
// provider (bucket name suffixes use random_id).
func SharedProviders() []TerraformProvider { return []TerraformProvider{PinRandom} }
