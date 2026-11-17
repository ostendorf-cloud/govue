#!/usr/bin/env bash
# Comment on the pull request when pnpm refused a locked package that is younger than
# its minimum release age. One comment per pull request, updated in place on a rerun.
# The log of any other failure is ignored and the script exits 0 without a gh call.
#
# Usage: release-age-comment.sh LOGFILE
# Environment: GH_TOKEN, REPO (owner/name), PR (pull request number)
set -euo pipefail

log=${1:?usage: release-age-comment.sh LOGFILE}
: "${REPO:?REPO is not set}" "${PR:?PR is not set}"

code=ERR_PNPM_MINIMUM_RELEASE_AGE_VIOLATION
marker='<!-- govue-release-age -->'

if [[ ! -f $log ]] || ! grep -q "$code" "$log"; then
  exit 0
fi

# pnpm 12 prints one entry per refused package, in this shape:
#   Error: ERR_PNPM_MINIMUM_RELEASE_AGE_VIOLATION
#     ╰─▶ 1 lockfile entries failed verification:
#           ignore@7.0.16 was published at 2026-11-17T05:56:55.170Z, within the
#         minimumReleaseAge cutoff (2026-11-16T23:05:09.237Z)
# The parse is defensive: an entry or a cutoff that is not found is left out.
# Colour codes are stripped first and only the text after the error code is read.
text=$(sed -E $'s/\x1b\\[[0-9;]*[A-Za-z]//g' "$log" | sed -n "/$code/,\$p")

name_re='(@[a-z0-9][a-z0-9._~-]*/)?[a-z0-9][a-z0-9._~-]*@[0-9][A-Za-z0-9.+-]*'
entries=$({ printf '%s\n' "$text" | grep -oE "$name_re was published at [0-9T:.Z-]+" || true; } |
  awk '!seen[$0]++' |
  while read -r name _ _ _ time; do
    printf '%s\n' "- \`$name\`, published at $time"
  done)
cutoff=$({ printf '%s\n' "$text" | grep -oE 'minimumReleaseAge cutoff \([^)]*\)' || true; } |
  head -n1 | sed -E 's/.*\(([^)]*)\)/\1/')

body=$(mktemp "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/govue-release-age.XXXXXX")
trap 'rm -f "$body"' EXIT

{
  printf '%s\n' "$marker"
  printf '%s\n' "The dependency install failed because a locked package is younger than pnpm's minimum release age. The default is one day."
  if [[ -n $entries ]]; then
    printf '\n%s\n%s\n' "Packages refused:" "$entries"
  fi
  if [[ -n $cutoff ]]; then
    printf '\n%s\n' "Cutoff of the failed run: $cutoff"
  fi
  printf '\n%s\n' "The install passes once every listed package was published before the cutoff of that run."
  printf '%s\n' "No change in the repository is needed. Rerun the failed jobs after that."
} >"$body"

# Find the comment this script wrote earlier. The author check keeps a copied marker
# in someone else's comment from being edited.
id=$(gh api --paginate "repos/$REPO/issues/$PR/comments" \
  --jq ".[] | select(.user.login == \"github-actions[bot]\" and (.body | contains(\"$marker\"))) | .id" | head -n1)

if [[ -n $id ]]; then
  gh api -X PATCH "repos/$REPO/issues/comments/$id" -F "body=@$body" >/dev/null
else
  gh api -X POST "repos/$REPO/issues/$PR/comments" -F "body=@$body" >/dev/null
fi
