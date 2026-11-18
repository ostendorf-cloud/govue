#!/usr/bin/env bash
# Test for the token boundary of the web-deps and web-deps-comment jobs in checks.yaml.
# The install job runs untrusted code and must never hold a write token or a secret. Only
# the comment job may. It mints a token as the dependencies app, with the private key of
# the app, and hands it to the shared pnpm release age comment action.
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
mint="$comment.steps[0]"
post="$comment.steps[1]"
# The pin of actions/create-github-app-token in reusable-renovate.yaml of the workflows
# repository. Keep both in step.
mint_uses='actions/create-github-app-token@64ab729a6294000f5aa4a4bc0b73d1f32bc45510'
mint_version='v3.3.0'
secret="\${{ secrets.GH_APP_DEPENDENCIES_PRIVATE_KEY }}"

# The workflow default stays read only. The secret never reaches a trigger that runs with
# secrets for pull request code: no pull_request_target and no workflow_run.
assert eq '.permissions | keys | join(",")' contents
assert eq '.permissions.contents' read
assert eq '.env' null
assert eq '.on | keys | sort | join(",")' pull_request,push
assert lacks '. | tojson' 'pull_request_target'
assert lacks '. | tojson' 'workflow_run'

# Exactly one job may write pull requests: the comment job.
assert eq '[.jobs[] | select(.permissions."pull-requests" == "write")] | length' 1
assert eq "$comment.permissions | keys | sort | join(\",\")" contents,pull-requests
assert eq "$comment.permissions.\"pull-requests\"" write
assert eq "$comment.permissions.contents" read
assert eq "$deps.permissions | keys | join(\",\")" contents
assert eq "$deps.permissions.contents" read
assert eq '.jobs["web-install"]' null

# Every permissions block is a map, so a string such as write-all cannot widen a job. No
# job but the comment job holds a write permission of any kind.
assert eq '[.jobs[] | select(.permissions != null and (.permissions | tag) != "!!map")] | length' 0
assert eq '[.jobs | to_entries[] | select(.key != "web-deps-comment") | (.value.permissions // {}) | to_entries[] | select(.value != "read" and .value != "none")] | length' 0

# The comment job has exactly these keys and runs on the hosted runner. No container, no
# service container and no other runner label on any job.
assert eq "$comment | keys | sort | join(\",\")" concurrency,if,needs,permissions,runs-on,steps
assert eq "$comment.\"runs-on\"" ubuntu-latest
assert eq '[.jobs[] | select(has("runs-on")) | select(."runs-on" != "ubuntu-latest")] | length' 0
assert eq '[.jobs[] | select(has("container") or has("services"))] | length' 0

# The install job holds no token of its own: no credential in the checkout, no token or
# secret in any key of any step, and never the comment action.
assert eq "$deps.steps[0].with.\"persist-credentials\"" false
assert lacks "$deps | tojson" 'github.token'
assert lacks "$deps | tojson" 'GH_TOKEN'
assert lacks "$deps | tojson" 'secrets.'
assert lacks "$deps | tojson" 'secrets'
assert lacks "$deps | tojson" 'vars.'
assert lacks "$deps | tojson" 'create-github-app-token'
assert lacks "$deps | tojson" 'pnpm-release-age-comment'
assert eq "$deps.secrets" null
assert eq "$deps.env" null

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

# Two steps, in this order: the token step, then the shared action at the floating first
# party tag, without a digest and without a version comment. The job checks out nothing
# and runs nothing of its own.
assert eq "$comment.steps | length" 2
assert eq "$post.uses" "$action@v0"
assert eq "$post.uses | line_comment" ''
assert lacks "$comment | tojson" 'actions/checkout'
assert eq "[$comment.steps[] | select(.run != null or .env != null)] | length" 0
assert eq "[$comment.steps[] | select(.\"working-directory\" != null or .shell != null)] | length" 0

# The token step is the app token action at the digest and version of reusable-renovate.yaml
# in the workflows repository. Its keys are exactly id, uses and with.
assert eq "$mint | keys | sort | join(\",\")" id,uses,with
assert eq "$mint.id" app-token
assert eq "$mint.uses" "$mint_uses"
assert eq "$mint.uses | line_comment" "$mint_version"
assert hex40 "$mint.uses"

# Its inputs are the app id, the private key and one permission. No owner and no
# repositories, so the token is scoped to this repository only. No other permission, no
# permission-contents or any other permission-* key, and no other input of the action.
assert eq "$mint.with | keys | sort | join(\",\")" app-id,permission-pull-requests,private-key
assert eq "$mint.with.\"app-id\"" "\${{ vars.GH_APP_DEPENDENCIES_APP_ID }}"
assert eq "$mint.with.\"private-key\"" "$secret"
assert eq "$mint.with.\"permission-pull-requests\"" write
assert eq "$mint.with.owner" null
assert eq "$mint.with.repositories" null
assert lacks "$mint | tojson" 'owner'
assert lacks "$mint | tojson" 'repositories'
assert eq "[$mint.with | keys | .[] | select(test(\"^permission-\"))] | join(\",\")" permission-pull-requests

# The shared action gets the minted token and the login of the app, so a rerun finds and
# updates the earlier comment. log-file stays at its default install.log, the file name the
# upload step uses. The artifact name is the one of the upload step.
assert eq "$post.with | keys | sort | join(\",\")" artifact-name,comment-author,github-token
assert eq "$post.with.\"github-token\"" "\${{ steps.app-token.outputs.token }}"
assert eq "$post.with.\"comment-author\"" 'ostendorf-cloud-dependencies[bot]'
assert eq "$post.with.\"artifact-name\"" "$(q "$upload | .with.name")"
assert eq "$post | keys | sort | join(\",\")" uses,with

# The secret and the variable appear once in the whole workflow, in the token step of the
# comment job and nowhere else. No job passes secrets on, and no step prints one.
assert eq '[.. | select(tag == "!!str" and test("secrets\\."))] | length' 1
# Any use of the secrets context counts, with or without the dot: toJSON(secrets), a
# secrets["x"] index. Only the private key reference may remain. The task name
# lint:secrets is no use of the context.
assert eq '[.. | select(tag == "!!str" and test("secrets") and (test("lint:secrets") | not))] | length' 1
assert eq '[.. | select(tag == "!!str" and test("vars\\."))] | length' 1
assert eq '[.. | select(tag == "!!str" and test("GH_APP_DEPENDENCIES"))] | length' 2
assert eq '[.jobs | to_entries | .[] | select(.value | tojson | test("secrets\\.")) | .key] | join(",")' web-deps-comment
assert eq '[.jobs[] | select(has("secrets"))] | length' 0
assert lacks '. | tojson' 'inherit'
assert lacks "$comment | tojson" 'ACTIONS_STEP_DEBUG'
assert eq "[$comment | .. | select(tag == \"!!str\" and test(\"add-mask|::debug|echo|printenv\"))] | length" 0

# No job uses the workflow token, in an expression or as GH_TOKEN or GITHUB_TOKEN. The
# minted token reaches the comment job only, and no step of any job sets a token itself.
assert lacks '. | tojson' 'github.token'
assert lacks '. | tojson' 'GITHUB_TOKEN'
assert eq '[.jobs[] | select(tojson | test("steps\\.app-token\\.outputs\\.token"))] | length' 1
assert eq '[.jobs[] | select(.steps != null) | .steps[] | select(.env.GH_TOKEN != null)] | length' 0

printf '%d checks, %d failed\n' "$checks" "$fails"
[[ $fails -eq 0 ]]
