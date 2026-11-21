#!/usr/bin/env bash
# Hermes AWS discovery demo against a LOCAL moto server. No real AWS is ever contacted:
# fake credentials, isolated AWS config files, and an endpoint bound to 127.0.0.1.
#
#   python3 -m venv .venv && .venv/bin/pip install 'moto[server]' boto3   # once (~180 MB)
#   VENV=.venv ./run-demo.sh
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/../../.." && pwd)"
VENV="${VENV:-$HERE/.venv}"
PORT="${MOTO_PORT:-5055}"
ENDPOINT="http://127.0.0.1:${PORT}"
REGION="eu-north-1"
WORK="${WORK:-$(mktemp -d "${TMPDIR:-/tmp}/hermes-demo.XXXXXX")}"
HERMES="${HERMES:-$WORK/hermes}"

# Credential isolation: nothing from ~/.aws is read, and only fake keys exist in this process.
export AWS_CONFIG_FILE=/dev/null AWS_SHARED_CREDENTIALS_FILE=/dev/null AWS_EC2_METADATA_DISABLED=true
export AWS_ACCESS_KEY_ID=testing AWS_SECRET_ACCESS_KEY=testing AWS_SESSION_TOKEN= AWS_DEFAULT_REGION="$REGION"
unset AWS_PROFILE AWS_ROLE_ARN AWS_WEB_IDENTITY_TOKEN_FILE AWS_ENDPOINT_URL 2>/dev/null || true

[ -x "$VENV/bin/moto_server" ] || { echo "moto not found in $VENV (see the header of this script)"; exit 1; }
echo "== work dir: $WORK"
[ -x "$HERMES" ] || (cd "$REPO" && go build -o "$HERMES" ./cmd/hermes)

"$VENV/bin/moto_server" -H 127.0.0.1 -p "$PORT" >"$WORK/moto.log" 2>&1 &
MOTO_PID=$!
trap 'kill $MOTO_PID 2>/dev/null || true' EXIT
for _ in $(seq 1 50); do curl -s -o /dev/null "$ENDPOINT/moto-api/" && break; sleep 0.2; done

echo "== seeding a hand-built environment (boto3 -> moto; no Terraform anywhere)"
"$HERMES" inventory policy --out "$WORK/policy.json"
"$VENV/bin/python" -I "$HERE/seed_moto.py" --endpoint "$ENDPOINT" --region "$REGION" --policy "$WORK/policy.json" \
  --external-id demo-external-id --operator-env "$WORK/operator.env"

set -a; . "$WORK/operator.env"; set +a   # fake keys created inside moto for the operator user
DISCOVER=("$HERMES" inventory aws --endpoint-url "$ENDPOINT" --region "$REGION" --regions "$REGION"
  --role-arn arn:aws:iam::123456789012:role/HermesDiscoveryReadOnly --external-id demo-external-id)

echo "== discovery: assume HermesDiscoveryReadOnly with an ExternalId, read-only APIs only"
"${DISCOVER[@]}" --out "$WORK/inventory.json" --operations-out "$WORK/operations.json"

echo "== discovery again with moto IAM enforcement on: denied calls become gaps, discovery continues"
curl -fsS -X POST -H 'Content-Type: text/plain' --data-binary 0 "$ENDPOINT/moto-api/reset-auth" >/dev/null
"${DISCOVER[@]}" --out "$WORK/inventory-enforced.json" >/dev/null 2>"$WORK/enforced.log" || true
curl -fsS -X POST -H 'Content-Type: text/plain' --data-binary inf "$ENDPOINT/moto-api/reset-auth" >/dev/null

echo "== assess, plan, generate (offline)"
"$HERMES" migrate assess --inventory "$WORK/inventory.json" --target gcp --project fleet-platform \
  --out "$WORK/assessment.json" --markdown "$WORK/assessment.md"
"$HERMES" migrate plan --assessment "$WORK/assessment.json" --env production --out "$WORK/migration-plan.yaml" --markdown "$WORK/plan.md"
"$HERMES" migrate generate --assessment "$WORK/assessment.json" --env production --out "$WORK/target"
"$HERMES" inventory graph --inventory "$WORK/inventory.json" --format mermaid --out "$WORK/graph.mmd"

if [ "${RUN_GO_TEST:-1}" = "1" ]; then
  echo "== Go integration test (build tag moto) against the same server"
  (cd "$REPO" && HERMES_MOTO_ENDPOINT="$ENDPOINT" HERMES_MOTO_REGION="$REGION" go test -count=1 -tags moto ./tests/integration/discovery/...)
fi

echo
echo "== summary"
python3 -I - "$WORK" <<'PY'
import json, sys, os
w = sys.argv[1]
inv = json.load(open(os.path.join(w, "inventory.json")))
enf = json.load(open(os.path.join(w, "inventory-enforced.json")))
a = json.load(open(os.path.join(w, "assessment.json")))
ops = json.load(open(os.path.join(w, "operations.json")))
print("resources: %d (%d types), relationships: %d, gaps: %d" % (inv["summary"]["resource_count"], len(inv["summary"]["by_type"]), len(inv["relationships"]), len(inv["gaps"])))
for g in inv["gaps"]:
    print("  gap: %s %s -> %s" % (g["service"], g["operation"], g["reason"]))
reasons = {}
for g in enf["gaps"]:
    reasons.setdefault(g["reason"], set()).add(g["service"] + ":" + g["operation"])
print("with IAM enforcement: %d resources, %d gaps: %s" % (enf["summary"]["resource_count"], len(enf["gaps"]),
      "; ".join("%s=%s" % (k, ",".join(sorted(v))) for k, v in sorted(reasons.items()))))
print("API operations called: %d distinct, all read-only (allowlisted by the guard)" % len(ops))
print("portability score (gcp): %.1f%%, waves: %d, blockers/risks: %d/%d" % (a["summary"]["portability_score"], a["summary"]["wave_count"], a["summary"]["blocker_count"], a["summary"]["risk_count"]))
print("secret value present in outputs: %s" % any("demo-not-a-secret" in open(os.path.join(w, f)).read() for f in ("inventory.json", "inventory-enforced.json", "assessment.json", "migration-plan.yaml")))
PY
echo "artifacts: $WORK"
