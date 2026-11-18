#!/usr/bin/env bash
# Test for the token boundary of the web-deps and web-deps-comment jobs in checks.yaml.
# The install job runs untrusted code and must never hold a write token. Only the comment
# job may, and it runs the shared pnpm release age comment action as its one step.
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
lacks() { [[ $(q "$1") != *"$2"* ]]; }
hex40() { [[ $(q "$1") =~ @[0-9a-f]{40}$ ]]; }

deps='.jobs["web-deps"]'
comment='.jobs["web-deps-comment"]'
upload="$deps.steps[] | select(.uses != null and (.uses | test(\"upload-artifact\")))"
action='ostendorf-cloud/workflows/.github/actions/pnpm-release-age-comment'

# The workflow default stays read only.
assert eq '.permissions | keys | join(",")' contents
assert eq '.permissions.contents' read
assert eq '.env' null

# Exactly one job may write pull requests: the comment job.
assert eq '[.jobs[] | select(.permissions."pull-requests" == "write")] | length' 1
assert eq "$comment.permissions | keys | sort | join(\",\")" contents,pull-requests
assert eq "$comment.permissions.\"pull-requests\"" write
assert eq "$comment.permissions.contents" read
assert eq "$deps.permissions | keys | join(\",\")" contents
assert eq "$deps.permissions.contents" read
assert eq '.jobs["web-install"]' null

# The install job holds no token of its own: no credential in the checkout, no token or
# secret in any key of any step, and never the comment action.
assert eq "$deps.steps[0].with.\"persist-credentials\"" false
assert lacks "$deps | tojson" 'github.token'
assert lacks "$deps | tojson" 'GH_TOKEN'
assert lacks "$deps | tojson" 'secrets.'
assert lacks "$deps | tojson" 'pnpm-release-age-comment'

# The install step keeps the exit code of the install, not the one of tee, and writes the
# log the upload step picks up.
install="$deps.steps[] | select(.name == \"Install the web dependencies and keep the log\") | .run"
assert eq "$install" "set -o pipefail
task web:deps 2>&1 | tee \"\$RUNNER_TEMP/install.log\""
assert eq "$upload | .if" 'failure()'
assert hex40 "$upload | .uses"
assert eq "$upload | .with.path" "\${{ runner.temp }}/install.log"
assert has "$upload | .with.name" 'github.run_attempt'

# The comment job runs after the install job, only for a failed install on a pull request
# of this repository, one run per pull request at a time.
assert eq "$comment.needs" web-deps
assert eq "$comment.if" "\${{ always() && needs.web-deps.result == 'failure' && github.event_name == 'pull_request' && github.event.pull_request.head.repo.full_name == github.repository }}"
assert eq "$comment.concurrency.\"cancel-in-progress\"" false
assert eq "$comment.concurrency.group" "release-age-comment-\${{ github.event.pull_request.number }}"

# Its one step is the shared action at the floating first party tag, without a digest and
# without a version comment. It checks out nothing and runs nothing of its own.
assert eq "$comment.steps | length" 1
assert eq "$comment.steps[0].uses" "$action@v0"
assert eq "$comment.steps[0].uses | line_comment" ''
assert lacks "$comment | tojson" 'actions/checkout'
assert eq "[$comment.steps[] | select(.run != null or .env != null)] | length" 0

# The inputs are the ones action.yaml defines. log-file stays at its default install.log,
# the file name the upload step uses. The artifact name is the one of the upload step.
assert eq "$comment.steps[0].with | keys | sort | join(\",\")" artifact-name,github-token
assert eq "$comment.steps[0].with.\"github-token\"" "\${{ github.token }}"
assert eq "$comment.steps[0].with.\"artifact-name\"" "$(q "$upload | .with.name")"

# The token reaches the comment job only, and no step of any job sets GH_TOKEN itself.
assert eq '[.jobs[] | select(tojson | test("github\\.token"))] | length' 1
assert eq '[.jobs[] | select(.steps != null) | .steps[] | select(.env.GH_TOKEN != null)] | length' 0

printf '%d checks, %d failed\n' "$checks" "$fails"
[[ $fails -eq 0 ]]
