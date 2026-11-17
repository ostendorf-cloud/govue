#!/usr/bin/env bash
# Test for release-age-comment.sh and install-with-log.sh. A stub gh first on PATH records
# every call, so no network is used.
#
# Usage: release-age-comment.test.sh
set -uo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
work=$(mktemp -d "${TMPDIR:-/tmp}/govue-release-age-test.XXXXXX")
trap 'rm -rf "$work"' EXIT

mkdir -p "$work/bin"
cat >"$work/bin/gh" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$STUB_DIR/calls"
for a in "$@"; do
  case $a in
    body=@*) cat "${a#body=@}" >"$STUB_DIR/body" ;;
  esac
done
case "$*" in
  *--jq*) printf '%s' "${STUB_COMMENT_ID:-}" ;;
esac
STUB
chmod +x "$work/bin/gh"

fails=0
checks=0

assert() {
  checks=$((checks + 1))
  if ! "$@"; then
    fails=$((fails + 1))
    printf 'FAIL: %s\n' "$*" >&2
  fi
}

calls_are() { [[ $(cat "$STUB_DIR/calls" 2>/dev/null) == "$1" ]]; }
no_calls() { [[ ! -e $STUB_DIR/calls ]]; }
no_post() { ! grep -qF 'POST' "$STUB_DIR/calls"; }
body_has() { grep -qF -- "$1" "$STUB_DIR/body"; }
body_lacks() { ! grep -qF -- "$1" "$STUB_DIR/body"; }

# run_case LOG_CONTENT [COMMENT_ID]: runs the script on a fresh stub state.
run_case() {
  STUB_DIR=$(mktemp -d "$work/case.XXXXXX")
  export STUB_DIR
  printf '%b' "$1" >"$STUB_DIR/install.log"
  STUB_COMMENT_ID=${2:-} PATH="$work/bin:$PATH" REPO=o/r PR=7 GH_TOKEN=x \
    bash "$here/release-age-comment.sh" "$STUB_DIR/install.log"
  status=$?
}

list_call='api --paginate repos/o/r/issues/7/comments --jq'

# 1. The real pnpm 12 failure of a pull request, no earlier comment: list, then one POST.
real_log='Error: ERR_PNPM_MINIMUM_RELEASE_AGE_VIOLATION

  × installing dependencies
  ╰─▶ 1 lockfile entries failed verification:
        ignore@7.0.16 was published at 2026-11-17T05:56:55.170Z, within the
      minimumReleaseAge cutoff (2026-11-16T23:05:09.237Z)
  help: The lockfile contains entries that the active policies reject. This
        can mean the lockfile is stale, or that someone committed a lockfile
        that bypassed the policy locally ...
'
run_case "WARN something first
$real_log"
assert test "$status" -eq 0
assert [ "$(wc -l <"$STUB_DIR/calls")" -eq 2 ]
assert grep -qF 'api -X POST repos/o/r/issues/7/comments -F body=@' "$STUB_DIR/calls"
assert body_has '<!-- govue-release-age -->'
assert body_has "- \`ignore@7.0.16\`, published at 2026-11-17T05:56:55.170Z"
assert body_has 'Cutoff of the failed run: 2026-11-16T23:05:09.237Z'
assert body_has 'published before the cutoff of that run'
assert body_has 'minimum release age'
assert body_has 'Rerun the failed jobs'
assert body_has 'No change in the repository is needed'

# 2. Two entries, one scoped, behind colour codes: every entry is listed once.
run_case '\033[31mError: ERR_PNPM_MINIMUM_RELEASE_AGE_VIOLATION\033[39m
  ╰─▶ 2 lockfile entries failed verification:
        \033[1mignore@7.0.16\033[22m was published at 2026-11-17T05:56:55.170Z, within the
      minimumReleaseAge cutoff (2026-11-16T23:05:09.237Z)
        @vue/shared@3.5.1-beta.2 was published at 2026-11-17T06:00:00.000Z, within the
      minimumReleaseAge cutoff (2026-11-16T23:05:09.237Z)
'
assert body_has "- \`ignore@7.0.16\`, published at 2026-11-17T05:56:55.170Z"
assert body_has "- \`@vue/shared@3.5.1-beta.2\`, published at 2026-11-17T06:00:00.000Z"
assert [ "$(grep -c '^- ' "$STUB_DIR/body")" -eq 2 ]
assert [ "$(grep -c 'Cutoff of the failed run' "$STUB_DIR/body")" -eq 1 ]

# 3. Error code, no entry in the log: the comment is still written, without a list.
run_case 'Error: ERR_PNPM_MINIMUM_RELEASE_AGE_VIOLATION something unparseable\n'
assert [ "$(wc -l <"$STUB_DIR/calls")" -eq 2 ]
assert body_has 'minimum release age'
assert body_lacks 'Packages refused'
assert body_lacks 'Cutoff of the failed run'

# 4. An entry before the error code is not read.
run_case 'old@1.0.0 was published at 2026-01-01T00:00:00.000Z\nError: ERR_PNPM_MINIMUM_RELEASE_AGE_VIOLATION\n'
assert body_lacks 'old@1.0.0'

# 5. An earlier comment exists: PATCH that comment, never a second POST.
run_case 'ERR_PNPM_MINIMUM_RELEASE_AGE_VIOLATION a@1.0.0\n' 4242
assert grep -qF 'api -X PATCH repos/o/r/issues/comments/4242 -F body=@' "$STUB_DIR/calls"
assert no_post
assert [ "$(wc -l <"$STUB_DIR/calls")" -eq 2 ]

# 6. The list call looks for the marker among comments of the workflow bot.
assert grep -qF "$list_call" "$STUB_DIR/calls"
assert grep -qF 'github-actions[bot]' "$STUB_DIR/calls"
assert grep -qF 'govue-release-age' "$STUB_DIR/calls"

# 7. Another failure: no gh call, exit 0.
run_case 'ERR_PNPM_FETCH_404 not found\n'
assert test "$status" -eq 0
assert no_calls

# 8. A missing log, for example when checkout failed: no gh call, exit 0.
STUB_DIR=$(mktemp -d "$work/case.XXXXXX")
export STUB_DIR
PATH="$work/bin:$PATH" REPO=o/r PR=7 bash "$here/release-age-comment.sh" "$work/none.log"
assert test "$?" -eq 0
assert no_calls

# 9. The exit code of the install is preserved and its output is logged.
bash "$here/install-with-log.sh" "$work/install.log" sh -c 'echo installing; exit 7' >/dev/null
assert test "$?" -eq 7
assert grep -qF installing "$work/install.log"
bash "$here/install-with-log.sh" "$work/install.log" sh -c 'echo fine; echo oops >&2' >/dev/null
assert test "$?" -eq 0
assert grep -qF oops "$work/install.log"

printf '%d checks, %d failed\n' "$checks" "$fails"
[[ $fails -eq 0 ]]
