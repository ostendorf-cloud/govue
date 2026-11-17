#!/usr/bin/env bash
# Comment on the pull request when pnpm refused a locked package that is younger than
# its minimum release age. One comment per pull request, updated in place on a rerun.
# The log of any other failure is ignored and the script exits 0 without a gh call.
#
# The log comes from a job that ran untrusted install code, so it is data: only text that
# matches a strict pattern reaches the comment.
#
# Usage: release-age-comment.sh LOGFILE
# Environment: GH_TOKEN, REPO (owner/name), PR (pull request number), and from the
# workflow GITHUB_SERVER_URL, GITHUB_REPOSITORY and GITHUB_RUN_ID for the run link.
set -euo pipefail

log=${1:?usage: release-age-comment.sh LOGFILE}
: "${REPO:?REPO is not set}" "${PR:?PR is not set}"

code=ERR_PNPM_MINIMUM_RELEASE_AGE_VIOLATION
marker='<!-- govue-release-age -->'
max_entries=50

if [[ ! -f $log ]] || ! grep -q "$code" "$log"; then
  exit 0
fi

# pnpm 12 prints one entry per refused package, in this shape, and wraps long lines:
#   Error: ERR_PNPM_MINIMUM_RELEASE_AGE_VIOLATION
#     ╰─▶ 1 lockfile entries failed verification:
#           ignore@7.0.16 was published at 2026-11-17T05:56:55.170Z, within the
#         minimumReleaseAge cutoff (2026-11-16T23:05:09.237Z)
# Colour codes are stripped, only the text after the error code is read, and it is joined
# into one line so a wrap anywhere does not lose an entry. An entry or a cutoff that is
# not found is left out.
text=$(sed -E $'s/\x1b\\[[0-9;]*[A-Za-z]//g' "$log" | sed -n "/$code/,\$p" | tr '\n' ' ' | tr -s ' ')

# The awk filter below drops a name longer than 214 characters, the npm limit.
name_re='(@[A-Za-z0-9._~-]+/)?[A-Za-z0-9._~-]+@[0-9][A-Za-z0-9.+-]*'
time_re='[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)?Z'

all=$({ printf '%s\n' "$text" | grep -oE "$name_re was published at $time_re" || true; } |
  awk '{ n = $1; sub(/@[^@]*$/, "", n) } length(n) <= 214 && !seen[$0]++' |
  while read -r name _ _ _ time; do
    printf '%s\n' "- \`$name\`, published at \`$time\`"
  done)
total=0
if [[ -n $all ]]; then
  total=$(printf '%s\n' "$all" | wc -l | tr -d ' ')
fi
entries=$(printf '%s\n' "$all" | sed -n "1,${max_entries}p")
if ((total > max_entries)); then
  entries+=$'\n'"- and $((total - max_entries)) more"
fi

cutoff=$({ printf '%s\n' "$text" | grep -oE "minimumReleaseAge cutoff \( *$time_re *\)" || true; } |
  sed -n '1p' | grep -oE "$time_re" || true)

run_url=
if [[ -n ${GITHUB_RUN_ID:-} ]]; then
  run_url="${GITHUB_SERVER_URL:-https://github.com}/${GITHUB_REPOSITORY:-$REPO}/actions/runs/$GITHUB_RUN_ID"
fi

body=$(mktemp "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/govue-release-age.XXXXXX")
trap 'rm -f "$body"' EXIT

{
  printf '%s\n' "$marker"
  printf '%s\n' "The dependency install failed because a locked package is younger than pnpm's minimumReleaseAge."
  if [[ -n $entries ]]; then
    printf '\n%s\n%s\n' "Packages refused:" "$entries"
  fi
  if [[ -n $cutoff ]]; then
    printf '\n%s\n' "Cutoff of the failed run: \`$cutoff\`"
  fi
  printf '\n%s\n' "The install passes once every listed package was published before the cutoff of that run."
  printf '%s\n' "No change in the repository is needed. Rerun the failed jobs after that."
  if [[ -n $run_url ]]; then
    printf '\n%s\n' "Run: $run_url"
  fi
  printf '%s\n' "This comment belongs to that run. If a later run passes, ignore it."
} >"$body"

# Find the comment this script wrote earlier. The author check keeps a copied marker
# in someone else's comment from being edited, and the first hit is the oldest.
id=$(gh api --paginate "repos/$REPO/issues/$PR/comments" \
  --jq ".[] | select(.user.login == \"github-actions[bot]\" and (.body | contains(\"$marker\"))) | .id" | sed -n '1p')

if [[ -n $id ]]; then
  gh api -X PATCH "repos/$REPO/issues/comments/$id" -F "body=@$body" >/dev/null
else
  gh api -X POST "repos/$REPO/issues/$PR/comments" -F "body=@$body" >/dev/null
fi
