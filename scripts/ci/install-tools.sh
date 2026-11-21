#!/usr/bin/env bash
# Install pinned, checksum-verified security tool binaries for CI (Linux x86_64).
#
# Why not the vendor GitHub Actions: in March 2026 most trivy-action and all setup-trivy tags were
# repointed to malicious commits (CVE-2026-33634), and gitleaks-action needs a license key for
# organisations. Release binaries pinned by version AND by a SHA-256 recorded here cannot be
# swapped by moving a tag or replacing a release asset. Bump versions deliberately: download the
# release's checksums file, verify it, update VERSION and SHA256 together, and keep a cooldown of
# at least 7 days after a release. Never use trivy v0.69.4 (the compromised release).
#
# Usage: scripts/ci/install-tools.sh <install-dir> [tool...]   (tools: gitleaks trivy conftest; default all)
set -euo pipefail

dest="${1:?usage: install-tools.sh <install-dir> [tool...]}"
shift || true
tools=("$@")
[ "${#tools[@]}" -gt 0 ] || tools=(gitleaks trivy conftest)

if [ "$(uname -s)-$(uname -m)" != "Linux-x86_64" ]; then
  echo "install-tools: only Linux x86_64 is pinned; install the tools yourself on $(uname -s)-$(uname -m)" >&2
  exit 2
fi

mkdir -p "$dest"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

install() { # name url sha256 binary-in-archive
  local name="$1" url="$2" sum="$3" bin="$4"
  echo "install-tools: $name from $url" >&2
  curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 --retry 3 -o "$work/$name.tgz" "$url"
  echo "$sum  $work/$name.tgz" | sha256sum --check --strict --quiet
  tar -xzf "$work/$name.tgz" -C "$work" "$bin"
  install -m 0755 "$work/$bin" "$dest/$bin"
}

for t in "${tools[@]}"; do
  case "$t" in
    gitleaks)
      v=8.30.1
      install gitleaks "https://github.com/gitleaks/gitleaks/releases/download/v$v/gitleaks_${v}_linux_x64.tar.gz" \
        551f6fc83ea457d62a0d98237cbad105af8d557003051f41f3e7ca7b3f2470eb gitleaks
      ;;
    trivy)
      v=0.76.2
      install trivy "https://github.com/aquasecurity/trivy/releases/download/v$v/trivy_${v}_Linux-64bit.tar.gz" \
        bc0fc726e017b8927a62b2d3531d7a84ee01769daf5c99ed866dced54089fdbf trivy
      ;;
    conftest)
      v=0.71.1
      install conftest "https://github.com/open-policy-agent/conftest/releases/download/v$v/conftest_${v}_Linux_x86_64.tar.gz" \
        c3f6b2a753bd56e377a1e51a95ae3e057f926f2e9139f7d5b5dfbbaf810f7985 conftest
      ;;
    *)
      echo "install-tools: unknown tool $t" >&2
      exit 2
      ;;
  esac
done
"$dest/${tools[0]}" --version >/dev/null 2>&1 || "$dest/${tools[0]}" version >/dev/null
