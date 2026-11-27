#!/usr/bin/env bash
# Test for the TypeScript hold in renovate.json. Renovate must propose no TypeScript 7 update
# until typescript-eslint and vue-tsc support it, and the rule must say when to remove it.
#
# Usage: renovate-config.test.sh
# Environment: CONFIG overrides the Renovate config under test.
set -uo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
cfg=${CONFIG:-$here/../../renovate.json}

fails=0
checks=0

assert() {
  checks=$((checks + 1))
  if ! "$@"; then
    fails=$((fails + 1))
    printf 'FAIL: %s\n' "$*" >&2
  fi
}

q() { yq -r "$1" "$cfg"; }
eq() { [[ $(q "$1") == "$2" ]]; }
has() { [[ $(q "$1") == *"$2"* ]]; }

rule='.packageRules[] | select((.matchPackageNames // []) | join(",") == "typescript")'

# Exactly one rule matches typescript alone.
assert eq "[$rule] | length" 1
assert eq "$rule | .allowedVersions" '<7'
# The description names the removal condition.
assert has "$rule | .description" 'Remove this rule when'
assert has "$rule | .description" 'typescript-eslint'
assert has "$rule | .description" 'vue-tsc'

printf '%d checks, %d failed\n' "$checks" "$fails"
[[ $fails -eq 0 ]]
