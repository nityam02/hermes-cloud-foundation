#!/usr/bin/env bash
# security-scan.sh — run every local security check and print a summary table.
#
# Usage: scripts/security-scan.sh [--strict] [--sarif] [--only check1,check2] [--terms FILE]
#
# Checks (in order):
#   gitleaks-dir    gitleaks over the working tree (.gitleaks.toml)
#   gitleaks-git    gitleaks over git history (only inside a git repository with commits)
#   trivy           trivy config with trivy.yaml, embedded checks only (--skip-check-update)
#   checkov         checkov with .checkov.yaml
#   tflint          tflint with .tflint.hcl in every infra module dir (plugins must be installed: set
#                   HERMES_TFLINT_INIT=1 to run `tflint --init`, which downloads pinned plugins)
#   conftest        conftest verify of the plan policies and their fixtures
#   workflow-pins   every GitHub Actions `uses:` is pinned to a full commit SHA (or is a local
#                   ./ action) and every workflow declares top-level `permissions:` (no tool needed)
#   forbidden-terms scripts/check-forbidden-terms.sh, only when a terms file is given
#                   (--terms or HERMES_FORBIDDEN_TERMS_FILE); otherwise reported as not configured
#
# Status per check: PASS, FAIL (findings or tool error; see the log), SKIP (tool not installed or
# not applicable). With --strict (CI), a skipped check whose tool is missing counts as a failure,
# so a broken runner can never produce a green scan. --sarif also writes SARIF reports for
# gitleaks and trivy. Logs and reports go to $HERMES_SCAN_OUT (default .hermes/security-scan).
#
# Cloud credentials are removed from the environment first: no check needs them, and none may
# use them (tflint deep checks stay disabled).
#
# Exit: 0 when every check passed or was legitimately skipped, 1 otherwise, 2 on usage errors.
set -uo pipefail

strict=0
sarif=0
only=""
terms="${HERMES_FORBIDDEN_TERMS_FILE:-}"
while [ $# -gt 0 ]; do
  case "$1" in
    --strict) strict=1 ;;
    --sarif) sarif=1 ;;
    --only)
      [ $# -ge 2 ] || { echo "security-scan: --only needs a value" >&2; exit 2; }
      only="$2"
      shift
      ;;
    --terms)
      [ $# -ge 2 ] || { echo "security-scan: --terms needs a value" >&2; exit 2; }
      terms="$2"
      shift
      ;;
    -h | --help)
      sed -n '2,25p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      echo "security-scan: unknown argument $1" >&2
      exit 2
      ;;
  esac
  shift
done

root="$(cd "$(dirname "$0")/.." && pwd -P)"
cd "$root" || exit 2
out="${HERMES_SCAN_OUT:-$root/.hermes/security-scan}"
mkdir -p "$out"

# Credential isolation.
export AWS_CONFIG_FILE=/dev/null AWS_SHARED_CREDENTIALS_FILE=/dev/null AWS_PROFILE= AWS_ACCESS_KEY_ID= \
  AWS_SECRET_ACCESS_KEY= AWS_SESSION_TOKEN= AWS_EC2_METADATA_DISABLED=true \
  GOOGLE_APPLICATION_CREDENTIALS=/dev/null GOOGLE_OAUTH_ACCESS_TOKEN= CLOUDSDK_CONFIG=/dev/null \
  ALIBABA_CLOUD_CREDENTIALS_FILE=/dev/null ALIBABA_CLOUD_ACCESS_KEY_ID= ALIBABA_CLOUD_ACCESS_KEY_SECRET= \
  ALICLOUD_ACCESS_KEY= ALICLOUD_SECRET_KEY=

names=()
statuses=()
details=()
failed=0

record() { # name status detail
  names+=("$1")
  statuses+=("$2")
  details+=("$3")
  case "$2" in
    FAIL) failed=1 ;;
    SKIP) case "$3" in "not installed"*) [ "$strict" -eq 1 ] && failed=1 ;; esac ;;
  esac
}

selected() {
  [ -z "$only" ] && return 0
  case ",$only," in *",$1,"*) return 0 ;; esac
  return 1
}

# run_check <name> <tool-on-PATH> <command...>
run_check() {
  local name="$1" tool="$2"
  shift 2
  selected "$name" || return 0
  if ! command -v "$tool" >/dev/null 2>&1; then
    record "$name" SKIP "not installed ($tool)"
    return 0
  fi
  local log="$out/$name.log"
  echo "==> $name" >&2
  if "$@" >"$log" 2>&1; then
    record "$name" PASS "${log#"$root"/}"
  else
    record "$name" FAIL "exit $? - see ${log#"$root"/}"
  fi
}

gitleaks_args=(--config .gitleaks.toml --redact --no-banner --exit-code 1)
if [ "$sarif" -eq 1 ]; then
  run_check gitleaks-dir gitleaks gitleaks dir . "${gitleaks_args[@]}" --report-format sarif --report-path "$out/gitleaks.sarif"
else
  run_check gitleaks-dir gitleaks gitleaks dir . "${gitleaks_args[@]}"
fi

if selected gitleaks-git; then
  if command -v git >/dev/null 2>&1 && git rev-parse --verify -q HEAD >/dev/null 2>&1; then
    run_check gitleaks-git gitleaks gitleaks git . "${gitleaks_args[@]}"
  else
    record gitleaks-git SKIP "no git history"
  fi
fi

trivy_args=(config --config trivy.yaml --skip-check-update --quiet --exit-code 1)
if [ "$sarif" -eq 1 ]; then
  run_check trivy trivy trivy "${trivy_args[@]}" --format sarif --output "$out/trivy.sarif" .
else
  run_check trivy trivy trivy "${trivy_args[@]}" .
fi

run_check checkov checkov checkov --config-file .checkov.yaml

tflint_scan() {
  if [ "${HERMES_TFLINT_INIT:-0}" = "1" ]; then
    tflint --init --config "$root/.tflint.hcl" || return 1
  fi
  # Lint every real module (a directory with versions.tf under infra/). Not --recursive: that
  # would also lint infra/contract (a declarations-only file) and rendered test fixtures.
  local d rc=0
  while IFS= read -r d; do
    echo "--- $d"
    tflint --chdir "$d" --config "$root/.tflint.hcl" --format compact || rc=1
  done < <(find infra -name versions.tf -not -path '*/.terraform/*' -exec dirname {} \; | sort)
  return "$rc"
}
run_check tflint tflint tflint_scan

run_check conftest conftest conftest verify --policy policies/plan --policy policies/tests --data policies/tests --no-color

workflow_pins() {
  local bad=0 f
  for f in .github/workflows/*.yml .github/workflows/*.yaml; do
    [ -f "$f" ] || continue
    if grep -n -E '^[[:space:]-]*uses:' "$f" | grep -v -E 'uses:[[:space:]]*(\./|[^@[:space:]]+@[0-9a-f]{40}([[:space:]]|$))'; then
      echo "$f: action not pinned to a full commit SHA (lines above)"
      bad=1
    fi
    if ! grep -q -E '^permissions:' "$f"; then
      echo "$f: no top-level permissions block"
      bad=1
    fi
  done
  return "$bad"
}
run_check workflow-pins bash workflow_pins

if selected forbidden-terms; then
  if [ -n "$terms" ]; then
    run_check forbidden-terms bash bash "$root/scripts/check-forbidden-terms.sh" "$terms"
  else
    record forbidden-terms SKIP "not configured (no terms file)"
  fi
fi

printf '\n%-16s %-6s %s\n' CHECK STATUS DETAIL
printf '%-16s %-6s %s\n' ---------------- ------ ------------------------------
if [ "${#names[@]}" -eq 0 ]; then
  echo "security-scan: no check selected" >&2
  exit 2
fi
for i in "${!names[@]}"; do
  printf '%-16s %-6s %s\n' "${names[$i]}" "${statuses[$i]}" "${details[$i]}"
done
if [ "$failed" -ne 0 ]; then
  echo "security-scan: FAILED" >&2
  exit 1
fi
echo "security-scan: OK" >&2
exit 0
