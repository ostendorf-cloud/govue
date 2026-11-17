#!/usr/bin/env bash
# Test for the token boundary of the web-deps and web-deps-comment jobs in checks.yaml.
# The install job runs untrusted code and must never hold a write token. Only the comment
# job may, and only for its one script step.
#
# Usage: checks-workflow.test.sh
# Environment: WORKFLOW overrides the workflow file under test.
set -uo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
wf=${WORKFLOW:-$here/../workflows/checks.yaml}

fails=0
checks=0

assert() {
  checks=$((checks + 1))
  if ! "$@"; then
    fails=$((fails + 1))
    printf 'FAIL: %s\n' "$*" >&2
  fi
}

q() { yq -r "$1" "$wf"; }
eq() { [[ $(q "$1") == "$2" ]]; }
has() { [[ $(q "$1") == *"$2"* ]]; }
hex40() { [[ $(q "$1") =~ @[0-9a-f]{40}$ ]]; }

deps='.jobs["web-deps"]'
comment='.jobs["web-deps-comment"]'

# The workflow default stays read only.
assert eq '.permissions | keys | join(",")' contents
assert eq '.permissions.contents' read

# Exactly one job may write pull requests: the comment job.
assert eq '[.jobs[] | select(.permissions."pull-requests" == "write")] | length' 1
assert eq "$comment.permissions.\"pull-requests\"" write
assert eq "$comment.permissions.contents" read
assert eq "$deps.permissions | keys | join(\",\")" contents
assert eq "$deps.permissions.contents" read
assert eq '.jobs["web-install"]' null

# The install job holds no token of its own: no credential in the checkout, no GH_TOKEN.
assert eq "$deps.steps[0].with.\"persist-credentials\"" false
assert eq "[$deps.steps[].env.GH_TOKEN | select(. != null)] | length" 0
assert has "$deps.steps[] | select(.run != null) | .run" install-with-log.sh
assert eq "$deps.steps[] | select(.uses != null and (.uses | test(\"upload-artifact\"))) | .if" 'failure()'
assert hex40 "$deps.steps[] | select(.uses != null and (.uses | test(\"upload-artifact\"))) | .uses"

# The comment job runs after the install job, only for a failed install on a pull request
# of this repository, with a fresh checkout of the scripts only and no stored credential.
assert eq "$comment.needs" web-deps
assert has "$comment.if" "needs.web-deps.result == 'failure'"
assert has "$comment.if" "github.event_name == 'pull_request'"
assert has "$comment.if" 'github.event.pull_request.head.repo.full_name == github.repository'
assert eq "$comment.steps[0].with.\"persist-credentials\"" false
assert eq "$comment.steps[0].with.\"sparse-checkout\"" .github/scripts
assert hex40 "$comment.steps[] | select(.uses != null and (.uses | test(\"download-artifact\"))) | .uses"
assert eq "$comment.concurrency.\"cancel-in-progress\"" false
assert has "$comment.concurrency.group" 'release-age-comment-'
assert has "$comment.concurrency.group" 'github.event.pull_request.number'

# The token reaches one step only: the script step of the comment job.
assert eq '[.jobs[].steps[] | select(.env.GH_TOKEN != null)] | length' 1
assert eq "$comment.steps[-1].env.GH_TOKEN" "\${{ github.token }}"
assert has "$comment.steps[-1].run" release-age-comment.sh

printf '%d checks, %d failed\n' "$checks" "$fails"
[[ $fails -eq 0 ]]
