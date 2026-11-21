#!/usr/bin/env bash
# Derive the policy regression fixtures policies/tests/fixtures/<provider>_default/plan.json from
# REAL plans of the provider compositions, so that module/policy drift fails `make policy-test`.
#
# How a fixture is derived (no cloud API is called):
#   1. Take the golden render of the multi-cloud example for that provider
#      (internal/render/testdata/golden/multi-cloud-<env>.tf.json: exactly what `hermes render`
#      writes for the default spec), drop the backend block and point module "platform" at the
#      composition in this repository.
#   2. `tofu test -json -verbose` a single `command = plan` run with mock providers
#      (tests/policy-fixtures/<provider>.tftest.hcl); -verbose emits the plan as `tofu show -json`
#      would (message type "test_plan").
#   3. Keep what the policies read: format_version, resource_changes (sorted by address),
#      configuration.root_module.module_calls.*.expressions.protected, and a stub planned_values.
#   4. Recompute the provider-computed tag/label maps the way the real providers do at plan time
#      from the rendered provider block: AWS tags_all = default_tags + tags; Google
#      terraform_labels and effective_labels = default_labels + labels. Mock providers return {}
#      for them, which would hide every Hermes tag from the policies.
#   Fidelity: mock providers fill the other computed attributes with deterministic mock values at
#   plan time (a real first plan shows many of them as unknown). Unknown-value handling is
#   covered by the hand-written `unknowns` fixture instead.
#
# Usage: scripts/gen-policy-fixtures.sh [--check]
#   --check  regenerate into a temporary directory and fail if the checked-in fixtures differ.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
TOFU=${TOFU:-tofu}
export TF_PLUGIN_CACHE_DIR=${TF_PLUGIN_CACHE_DIR:-$HOME/.cache/hermes-tf-plugins}
mkdir -p "$TF_PLUGIN_CACHE_DIR"

check=false
if [[ "${1:-}" == "--check" ]]; then check=true; fi

out_root="$ROOT/policies/tests/fixtures"
if $check; then out_root=$(mktemp -d); fi

# Credential isolation: the mock providers never call a cloud API, but nothing may pick up real
# credentials even by accident.
isolate=(env AWS_CONFIG_FILE=/dev/null AWS_SHARED_CREDENTIALS_FILE=/dev/null AWS_PROFILE=
  AWS_ACCESS_KEY_ID= AWS_SECRET_ACCESS_KEY= AWS_SESSION_TOKEN= AWS_EC2_METADATA_DISABLED=true
  GOOGLE_APPLICATION_CREDENTIALS=/dev/null CLOUDSDK_CONFIG=/dev/null
  ALIBABA_CLOUD_CREDENTIALS_FILE=/dev/null ALIBABA_CLOUD_ACCESS_KEY_ID= ALIBABA_CLOUD_ACCESS_KEY_SECRET=)

status=0
for pair in aws:awsdev gcp:gcpdev alibaba:alidev; do
  provider=${pair%%:*}
  env=${pair##*:}
  golden="$ROOT/internal/render/testdata/golden/multi-cloud-$env.tf.json"
  # Inside the gitignored .hermes/ so the module source can be a fixed relative path.
  work="$ROOT/.hermes/policy-fixtures/$provider"
  rm -rf "$work"
  mkdir -p "$work/tests"
  jq --arg src "../../../infra/providers/$provider/platform" \
    'del(.terraform.backend) | .module.platform.source = $src' "$golden" >"$work/main.tf.json"
  cp "$ROOT/tests/policy-fixtures/$provider.tftest.hcl" "$work/tests/plan.tftest.hcl"

  "${isolate[@]}" "$TOFU" -chdir="$work" init -backend=false -input=false -no-color >/dev/null
  "${isolate[@]}" "$TOFU" -chdir="$work" test -json -verbose -no-color >"$work/test.jsonl" || {
    echo "tofu test failed for $provider; see $work/test.jsonl" >&2
    exit 1
  }

  mkdir -p "$out_root/${provider}_default"
  jq -S --arg env "$env" --slurpfile root "$work/main.tf.json" '
    ($root[0].provider.aws.default_tags.tags // {}) as $aws_tags |
    ($root[0].provider.google.default_labels // {}) as $google_labels |
    def provider_labels:
      if (.type | startswith("aws_")) and (.change.after | type) == "object" and (.change.after | has("tags_all")) then
        .change.after.tags_all = ($aws_tags + (.change.after.tags // {}))
      elif (.type | startswith("google_")) and (.change.after | type) == "object" then
        reduce ("terraform_labels", "effective_labels") as $k (.;
          if .change.after | has($k) then .change.after[$k] = ($google_labels + (.change.after.labels // {})) else . end)
      else . end;
    select(.type == "test_plan") | .test_plan | {
      "//": ("Derived by scripts/gen-policy-fixtures.sh from a mock-provider plan of the golden render of examples/multi-cloud (environment " + $env + "). Do not edit by hand: regenerate."),
      format_version,
      planned_values: {root_module: {}},
      configuration: {root_module: {module_calls: (.configuration.root_module.module_calls | map_values({source, expressions: {protected: .expressions.protected}}))}},
      resource_changes: ([.resource_changes[] | select(.mode == "managed") | {address, module_address, mode, type, name, index, change: {actions: .change.actions, after: .change.after, after_unknown: .change.after_unknown}} | provider_labels] | sort_by(.address))
    }' "$work/test.jsonl" >"$out_root/${provider}_default/plan.json"

  if [[ ! -s "$out_root/${provider}_default/plan.json" ]]; then
    echo "no test_plan message for $provider" >&2
    exit 1
  fi
  rm -rf "$work"

  if $check; then
    if ! diff -q "$ROOT/policies/tests/fixtures/${provider}_default/plan.json" "$out_root/${provider}_default/plan.json" >/dev/null; then
      echo "policies/tests/fixtures/${provider}_default/plan.json is stale: run scripts/gen-policy-fixtures.sh" >&2
      status=1
    else
      echo "${provider}_default: up to date"
    fi
  else
    echo "wrote policies/tests/fixtures/${provider}_default/plan.json ($(jq '.resource_changes | length' "$out_root/${provider}_default/plan.json") resources)"
  fi
done
rmdir "$ROOT/.hermes/policy-fixtures" 2>/dev/null || true
if $check; then rm -rf "$out_root"; fi
exit $status
