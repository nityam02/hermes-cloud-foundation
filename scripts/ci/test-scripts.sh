#!/usr/bin/env bash
# Self-test for scripts/check-forbidden-terms.sh and scripts/security-scan.sh.
# Uses temporary directories, a temporary terms file with invented terms, and stub tools on PATH
# (including a stub `git`, so the git code path is exercised without running git).
# Usage: scripts/ci/test-scripts.sh    Exit 0 when every case passes.
set -uo pipefail

here="$(cd "$(dirname "$0")/../.." && pwd -P)"
terms_script="$here/scripts/check-forbidden-terms.sh"
scan_script="$here/scripts/security-scan.sh"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/hermes-script-test.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT

pass=0
fail=0
check() { # description expected-exit actual-exit
  if [ "$2" = "$3" ]; then
    pass=$((pass + 1))
  else
    fail=$((fail + 1))
    echo "FAIL: $1 (expected exit $2, got $3)" >&2
  fi
}
contains() { # description haystack needle
  case "$2" in *"$3"*) pass=$((pass + 1)) ;; *) fail=$((fail + 1)); echo "FAIL: $1 (missing: $3)" >&2 ;; esac
}
absent() { # description haystack needle
  case "$2" in *"$3"*) fail=$((fail + 1)); echo "FAIL: $1 (unexpected: $3)" >&2 ;; *) pass=$((pass + 1)) ;; esac
}

# Stub bin dir with a `git` that is NOT a work tree, so the find fallback is used.
nogit="$tmp/nogit-bin"
mkdir -p "$nogit"
printf '#!/bin/sh\nexit 128\n' >"$nogit/git"
chmod +x "$nogit/git"

# ---------------------------------------------------------------- forbidden terms
tree="$tmp/tree"
mkdir -p "$tree/docs" "$tree/.terraform" "$tree/img"
printf 'This mentions Zorblax Industries in passing.\nclean line\n' >"$tree/docs/notes.md"
printf 'nothing to see\n' >"$tree/README.md"
printf 'zorblax inside a .terraform dir is ignored\n' >"$tree/.terraform/x.txt"
printf 'a.b literal\n' >"$tree/regex.txt"
printf '\x00\x01zorblax\x00binary' >"$tree/img/blob.bin"
printf 'project-quillfeather plan\n' >"$tree/docs/quillfeather-notes.txt"

terms="$tmp/terms.txt"
printf '# private denylist (invented terms for the test)\n\n  Zorblax  \nquillfeather\na.b\n' >"$terms"

run_terms() { (cd "$tree" && PATH="$nogit:$PATH" bash "$terms_script" "$@" 2>&1); }

out="$(run_terms)"; check "no terms file -> usage error" 2 $?
out="$(run_terms "$tmp/missing.txt")"; check "missing terms file -> error" 2 $?
printf '# only comments\n\n' >"$tmp/empty.txt"
out="$(run_terms "$tmp/empty.txt")"; check "empty terms file -> error" 2 $?

out="$(run_terms "$terms")"; rc=$?
check "hits -> exit 1" 1 "$rc"
contains "content hit reported with file:line" "$out" "docs/notes.md:1: forbidden term #3"
contains "path hit reported" "$out" "docs/quillfeather-notes.txt:path: forbidden term #4"
contains "content hit in second file" "$out" "docs/quillfeather-notes.txt:1: forbidden term #4"
contains "fixed-string term matched literally" "$out" "regex.txt:1: forbidden term #5"
absent "term text not printed by default" "$out" "Zorblax"
absent "matching line not printed" "$out" "in passing"
absent ".terraform ignored" "$out" ".terraform/x.txt"
absent "binary files skipped" "$out" "blob.bin"

out="$(run_terms --show-terms "$terms")"
contains "--show-terms prints the matched text" "$out" "(Zorblax)"

printf 'axb only\n' >"$tree/regex.txt"
out="$(run_terms "$terms")"
absent "regex metacharacters are not regex" "$out" "regex.txt"

out="$(cd "$tree" && PATH="$nogit:$PATH" HERMES_FORBIDDEN_TERMS_FILE="$terms" bash "$terms_script" 2>&1)"
check "terms file from environment" 1 $?

clean="$tmp/clean"
mkdir -p "$clean"
printf 'all good\n' >"$clean/a.txt"
out="$(cd "$clean" && PATH="$nogit:$PATH" bash "$terms_script" "$terms" 2>&1)"; check "clean tree -> exit 0" 0 $?

cp "$terms" "$clean/terms.txt"
out="$(cd "$clean" && PATH="$nogit:$PATH" bash "$terms_script" "$clean/terms.txt" 2>&1)"; check "terms file inside tree -> refused" 2 $?
rm "$clean/terms.txt"

# Git path: a stub git reports a work tree and lists only some files (ignored files are not listed).
gitbin="$tmp/git-bin"
mkdir -p "$gitbin"
cat >"$gitbin/git" <<'STUB'
#!/bin/sh
case "$*" in
  "rev-parse --is-inside-work-tree") echo true ;;
  "ls-files -z -co --exclude-standard") printf 'README.md\0docs/notes.md\0deleted-but-tracked.md\0' ;;
  *) exit 1 ;;
esac
STUB
chmod +x "$gitbin/git"
out="$(cd "$tree" && PATH="$gitbin:$PATH" bash "$terms_script" "$terms" 2>&1)"; rc=$?
check "git mode -> hit" 1 "$rc"
contains "git mode scans listed files" "$out" "docs/notes.md:1:"
absent "git mode skips unlisted files" "$out" "quillfeather-notes.txt"

# ---------------------------------------------------------------- security-scan summary logic
stubs="$tmp/stubs"
mkdir -p "$stubs"
for t in gitleaks trivy checkov tflint conftest; do
  printf '#!/bin/sh\necho "%s stub $*"\nexit "${STUB_%s_EXIT:-0}"\n' "$t" "$(echo "$t" | tr a-z A-Z)" >"$stubs/$t"
  chmod +x "$stubs/$t"
done
cp "$nogit/git" "$stubs/git"
base_path="$stubs:/usr/bin:/bin"

out="$(PATH="$base_path" HERMES_SCAN_OUT="$tmp/scan1" bash "$scan_script" 2>&1)"; rc=$?
check "all stub tools pass -> exit 0" 0 "$rc"
contains "summary lists trivy" "$out" "trivy            PASS"
contains "forbidden terms not configured" "$out" "not configured"
contains "no git history skipped" "$out" "no git history"

out="$(PATH="$base_path" STUB_TRIVY_EXIT=1 HERMES_SCAN_OUT="$tmp/scan2" bash "$scan_script" 2>&1)"; rc=$?
check "a finding -> exit 1" 1 "$rc"
contains "failing check reported" "$out" "trivy            FAIL"

rm "$stubs/checkov"
out="$(PATH="$base_path" HERMES_SCAN_OUT="$tmp/scan3" bash "$scan_script" 2>&1)"; check "missing tool, non-strict -> exit 0" 0 $?
contains "missing tool skipped" "$out" "checkov          SKIP   not installed"
out="$(PATH="$base_path" HERMES_SCAN_OUT="$tmp/scan4" bash "$scan_script" --strict 2>&1)"; check "missing tool, strict -> exit 1" 1 $?

# A term built at run time, so it cannot appear in the repository (not even in this file).
printf 'qz%sxq\n' "$$$RANDOM" >"$tmp/repo-terms.txt"
out="$(PATH="$base_path" HERMES_SCAN_OUT="$tmp/scan5" bash "$scan_script" --only conftest,forbidden-terms --terms "$tmp/repo-terms.txt" 2>&1)"
check "repository has none of the invented terms" 0 $?
contains "forbidden terms runs when configured" "$out" "forbidden-terms  PASS"
contains "--only limits checks" "$out" "conftest         PASS"
absent "--only excludes others" "$out" "gitleaks-dir"

out="$(PATH="$base_path" bash "$scan_script" --bogus 2>&1)"; check "unknown argument -> exit 2" 2 $?

# ---------------------------------------------------------------- install-tools.sh
# Regression test: the installer once defined a shell function named `install`, which shadowed the
# install(1) command and recursed into itself ("curl: Protocol http not supported"). It runs only on
# Linux x86_64, so uname, curl and sha256sum are stubbed here; tar and install are the real tools.
inst="$here/scripts/ci/install-tools.sh"
shims="$tmp/install-shims"
fixture="$tmp/install-fixture"
mkdir -p "$shims" "$fixture/src"
printf '#!/bin/sh\necho "fake tool 1.0"\n' >"$fixture/src/conftest"
chmod +x "$fixture/src/conftest"
tar -czf "$fixture/conftest.tgz" -C "$fixture/src" conftest
printf '#!/bin/sh\ncase "$1" in -s) echo Linux ;; -m) echo x86_64 ;; *) echo Linux ;; esac\n' >"$shims/uname"
# curl stub: record the URL, then copy the fixture archive to the -o target.
printf '#!/bin/sh\nout=""; url=""\nwhile [ $# -gt 0 ]; do case "$1" in -o) out="$2"; shift ;; http*) url="$1" ;; esac; shift; done\necho "$url" >>"%s/urls"\ncp "%s/conftest.tgz" "$out"\n' "$fixture" "$fixture" >"$shims/curl"
printf '#!/bin/sh\ncat >/dev/null\nexit 0\n' >"$shims/sha256sum"
chmod +x "$shims/uname" "$shims/curl" "$shims/sha256sum"

out="$(PATH="$shims:$PATH" bash "$inst" "$tmp/install-dest" conftest 2>&1)"; rc=$?
check "install-tools installs a pinned tool (stubbed network)" 0 "$rc"
contains "install-tools downloads over https from the release" "$(cat "$fixture/urls" 2>/dev/null)" "https://github.com/open-policy-agent/conftest/releases/download/"
check "installed binary is executable" 0 "$([ -x "$tmp/install-dest/conftest" ] && echo 0 || echo 1)"
absent "install-tools never recurses into itself" "$out" "Protocol"

out="$(PATH="$shims:$PATH" bash "$inst" "$tmp/install-dest" nosuchtool 2>&1)"; rc=$?
check "install-tools rejects unknown tools" 2 "$rc"

echo "test-scripts: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
