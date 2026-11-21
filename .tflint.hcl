# TFLint configuration for every module under infra/ (run: scripts/security-scan.sh --only tflint, which lints each module dir).
# Plugins are pinned to exact versions; `tflint --init` verifies the signature of official
# terraform-linters rulesets. There is no official Alibaba Cloud ruleset (the conftest policies in
# policies/plan cover the critical Alibaba cases instead).
# deep_check stays disabled: it would call cloud APIs with whatever credentials are present.

config {
  call_module_type = "local"
}

plugin "terraform" {
  enabled = true
  preset  = "recommended"
}

plugin "aws" {
  enabled    = true
  version    = "0.50.0"
  source     = "github.com/terraform-linters/tflint-ruleset-aws"
  deep_check = false
}

plugin "google" {
  enabled    = true
  version    = "0.41.0"
  source     = "github.com/terraform-linters/tflint-ruleset-google"
  deep_check = false
}
