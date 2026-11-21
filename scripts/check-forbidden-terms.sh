#!/usr/bin/env bash
# check-forbidden-terms.sh — fail when the working tree mentions any term from a PRIVATE denylist.
#
# Organisations keep a private list of confidential names (customers, internal projects, code
# names) that must never appear in a public repository. This repository contains no such list.
#
# Usage:
#   scripts/check-forbidden-terms.sh [--show-terms] [TERMS_FILE]
#   HERMES_FORBIDDEN_TERMS_FILE=/secure/path/terms.txt scripts/check-forbidden-terms.sh
#
# Terms file: one term per line, matched case-insensitively as a fixed string (no regex).
# Blank lines and lines starting with '#' are ignored; surrounding whitespace is trimmed.
#
# Scope: tracked and untracked-but-not-ignored files (`git ls-files -co --exclude-standard`).
# Outside a git work tree it falls back to every regular file below the current directory,
# excluding .git, .hermes, .terraform, bin, dist and node_modules. Binary files are skipped; file
# paths are checked too.
#
# Output: `<file>:<line>: forbidden term #<n>` per hit, where <n> is the term's line number in
# the terms file. The term itself and the matching line are NOT printed (CI logs of public
# repositories are public); pass --show-terms locally to include the matched term.
#
# Exit codes: 0 no hits, 1 hits found, 2 usage or configuration error.
set -euo pipefail

show_terms=0
terms_file="${HERMES_FORBIDDEN_TERMS_FILE:-}"

while [ $# -gt 0 ]; do
  case "$1" in
    --show-terms) show_terms=1 ;;
    -h | --help)
      sed -n '2,24p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    -*)
      echo "check-forbidden-terms: unknown option $1" >&2
      exit 2
      ;;
    *) terms_file="$1" ;;
  esac
  shift
done

if [ -z "$terms_file" ]; then
  echo "check-forbidden-terms: no terms file (pass a path or set HERMES_FORBIDDEN_TERMS_FILE)" >&2
  exit 2
fi
if [ ! -f "$terms_file" ] || [ ! -r "$terms_file" ]; then
  echo "check-forbidden-terms: terms file is not a readable file" >&2
  exit 2
fi

workdir="$(mktemp -d "${TMPDIR:-/tmp}/hermes-terms.XXXXXX")"
chmod 700 "$workdir"
trap 'rm -rf "$workdir"' EXIT

# Clean term list (lower-cased) and an index file "<terms-file-line>\t<term>" for reporting.
awk '{
  line = $0
  sub(/^[ \t\r]+/, "", line); sub(/[ \t\r]+$/, "", line)
  if (line == "" || substr(line, 1, 1) == "#") next
  print NR "\t" tolower(line)
}' "$terms_file" >"$workdir/index"
cut -f2- "$workdir/index" >"$workdir/terms"

if [ ! -s "$workdir/terms" ]; then
  echo "check-forbidden-terms: the terms file contains no terms" >&2
  exit 2
fi

# File list (NUL-separated).
if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  git ls-files -z -co --exclude-standard >"$workdir/files"
else
  echo "check-forbidden-terms: not a git work tree; scanning all files below $(pwd)" >&2
  find . \( -name .git -o -name .hermes -o -name .terraform -o -name node_modules -o -path ./bin -o -path ./dist \) -prune \
    -o -type f -print0 | while IFS= read -r -d '' f; do printf '%s\0' "${f#./}"; done >"$workdir/files"
fi

# Refuse to run when the terms file itself is part of the scanned tree: it must never be committed.
terms_base="${terms_file##*/}"
terms_real="$(cd "$(dirname "$terms_file")" && pwd -P)/$terms_base"
while IFS= read -r -d '' f; do
  [ "${f##*/}" = "$terms_base" ] && [ -e "$f" ] || continue
  if [ "$(cd "$(dirname "$f")" && pwd -P)/$terms_base" = "$terms_real" ]; then
    echo "check-forbidden-terms: the terms file is inside the scanned tree and not ignored; move it outside the repository" >&2
    exit 2
  fi
done <"$workdir/files"

# Report helper: reads "<file>\t<line>\t<matched text>" and maps the match back to the term number.
report() {
  awk -F'\t' -v show="$show_terms" -v index_file="$workdir/index" '
    BEGIN { while ((getline l < index_file) > 0) { n = l; sub(/\t.*/, "", n); num[substr(l, length(n) + 2)] = n } }
    {
      m = tolower($3)
      id = (m in num) ? num[m] : "?"
      if (show == 1) printf "%s:%s: forbidden term #%s (%s)\n", $1, $2, id, $3
      else printf "%s:%s: forbidden term #%s\n", $1, $2, id
    }'
}

: >"$workdir/hits"

# 1. File paths.
tr '\0' '\n' <"$workdir/files" | { grep -i -F -f "$workdir/terms" || true; } | while IFS= read -r path; do
  printf '%s\n' "$path" | grep -i -F -o -f "$workdir/terms" | while IFS= read -r match; do
    printf '%s\tpath\t%s\n' "$path" "$match"
  done
done >>"$workdir/hits"

# 2. File contents (text files only): find candidate files in bulk, then report each matching line.
xargs -0 grep -l -I -i -F -f "$workdir/terms" -- <"$workdir/files" 2>/dev/null >"$workdir/candidates" || true
while IFS= read -r f; do
  [ -f "$f" ] || continue
  grep -n -o -I -i -F -f "$workdir/terms" -- "$f" 2>/dev/null | while IFS= read -r l; do
    printf '%s\t%s\t%s\n' "$f" "${l%%:*}" "${l#*:}"
  done >>"$workdir/hits"
done <"$workdir/candidates"

if [ -s "$workdir/hits" ]; then
  sort -u "$workdir/hits" | report
  hits="$(sort -u "$workdir/hits" | wc -l | tr -d ' ')"
  echo "check-forbidden-terms: $hits hit(s)" >&2
  exit 1
fi

echo "check-forbidden-terms: no forbidden terms found" >&2
exit 0
