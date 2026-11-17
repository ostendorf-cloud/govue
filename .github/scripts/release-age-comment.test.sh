#!/usr/bin/env bash
# Test for release-age-comment.sh and install-with-log.sh. A stub gh first on PATH records
# every call and applies a --jq expression with the real jq to a fixture comment list, so
# no network is used.
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
expr=
prev=
for a in "$@"; do
  case $a in
    body=@*) cat "${a#body=@}" >"$STUB_DIR/body" ;;
  esac
  if [[ $prev == --jq ]]; then
    expr=$a
  fi
  prev=$a
done
if [[ -n $expr && -f $STUB_DIR/comments.json ]]; then
  jq -r "$expr" "$STUB_DIR/comments.json"
fi
exit 0
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

no_calls() { [[ ! -e $STUB_DIR/calls ]]; }
no_patch() { ! grep -qF 'PATCH' "$STUB_DIR/calls"; }
no_post() { ! grep -qF 'POST' "$STUB_DIR/calls"; }
body_has() { grep -qF -- "$1" "$STUB_DIR/body"; }
body_lacks() { ! grep -qF -- "$1" "$STUB_DIR/body"; }
call_count() { wc -l <"$STUB_DIR/calls" | tr -d ' '; }

# run_case LOG_CONTENT [COMMENTS_JSON]: runs the script on a fresh stub state.
run_case() {
  STUB_DIR=$(mktemp -d "$work/case.XXXXXX")
  export STUB_DIR
  printf '%b' "$1" >"$STUB_DIR/install.log"
  if [[ -n ${2:-} ]]; then
    printf '%s' "$2" >"$STUB_DIR/comments.json"
  fi
  PATH="$work/bin:$PATH" REPO=o/r PR=7 GH_TOKEN=x GITHUB_SERVER_URL=https://github.com \
    GITHUB_REPOSITORY=o/r GITHUB_RUN_ID=99 \
    bash "$here/release-age-comment.sh" "$STUB_DIR/install.log"
  status=$?
}

marker='<!-- govue-release-age -->'
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
assert test "$(call_count)" -eq 2
assert grep -qF 'api -X POST repos/o/r/issues/7/comments -F body=@' "$STUB_DIR/calls"
assert body_has "$marker"
assert body_has "- \`ignore@7.0.16\`, published at \`2026-11-17T05:56:55.170Z\`"
assert body_has "Cutoff of the failed run: \`2026-11-16T23:05:09.237Z\`"
assert body_has 'published before the cutoff of that run'
assert body_has "minimumReleaseAge"
assert body_has 'Rerun the failed jobs'
assert body_has 'No change in the repository is needed'
# The policy age is not claimed anywhere, it differs per repository.
assert body_lacks 'one day'
assert body_lacks 'default'
# The comment points at its run and says it goes stale.
assert body_has 'https://github.com/o/r/actions/runs/99'
assert body_has 'This comment belongs to that run. If a later run passes, ignore it.'

# 2. Two entries, one scoped, behind colour codes: every entry is listed once.
run_case '\033[31mError: ERR_PNPM_MINIMUM_RELEASE_AGE_VIOLATION\033[39m
  ╰─▶ 2 lockfile entries failed verification:
        \033[1mignore@7.0.16\033[22m was published at 2026-11-17T05:56:55.170Z, within the
      minimumReleaseAge cutoff (2026-11-16T23:05:09.237Z)
        @vue/shared@3.5.1-beta.2 was published at 2026-11-17T06:00:00.000Z, within the
      minimumReleaseAge cutoff (2026-11-16T23:05:09.237Z)
'
assert body_has "- \`ignore@7.0.16\`, published at \`2026-11-17T05:56:55.170Z\`"
assert body_has "- \`@vue/shared@3.5.1-beta.2\`, published at \`2026-11-17T06:00:00.000Z\`"
assert test "$(grep -c '^- ' "$STUB_DIR/body")" -eq 2
assert test "$(grep -c 'Cutoff of the failed run' "$STUB_DIR/body")" -eq 1

# 3. Error code, no entry in the log: the comment is still written, without a list.
run_case 'Error: ERR_PNPM_MINIMUM_RELEASE_AGE_VIOLATION something unparseable\n'
assert test "$(call_count)" -eq 2
assert body_has 'minimumReleaseAge'
assert body_lacks 'Packages refused'
assert body_lacks 'Cutoff of the failed run'

# 4. An entry before the error code is not read.
run_case 'old@1.0.0 was published at 2026-01-01T00:00:00.000Z\nError: ERR_PNPM_MINIMUM_RELEASE_AGE_VIOLATION\n'
assert body_lacks 'old@1.0.0'

# 5. An earlier comment of the bot exists: PATCH that comment, never a second POST.
bot='{"id":4242,"user":{"login":"github-actions[bot]"},"body":"<!-- govue-release-age -->\\nold"}'
run_case 'Error: ERR_PNPM_MINIMUM_RELEASE_AGE_VIOLATION a@1.0.0\n' "[$bot]"
assert grep -qF 'api -X PATCH repos/o/r/issues/comments/4242 -F body=@' "$STUB_DIR/calls"
assert no_post
assert test "$(call_count)" -eq 2

# 6. The list call asks for the comments of the pull request.
assert grep -qF "$list_call" "$STUB_DIR/calls"

# 7. Several comments: a human copy of the marker and a bot comment without it are never
# touched, and the oldest of two bot comments with the marker is the one updated.
human='{"id":1,"user":{"login":"someone"},"body":"<!-- govue-release-age -->\\ncopied"}'
other='{"id":2,"user":{"login":"github-actions[bot]"},"body":"a different bot comment"}'
older='{"id":3,"user":{"login":"github-actions[bot]"},"body":"<!-- govue-release-age -->\\nfirst"}'
newer='{"id":4,"user":{"login":"github-actions[bot]"},"body":"<!-- govue-release-age -->\\nsecond"}'
run_case 'Error: ERR_PNPM_MINIMUM_RELEASE_AGE_VIOLATION a@1.0.0\n' "[$human,$other,$older,$newer]"
assert grep -qF 'api -X PATCH repos/o/r/issues/comments/3 -F body=@' "$STUB_DIR/calls"
assert test "$(call_count)" -eq 2
# Only a human copy and an unrelated bot comment: a new comment is posted.
run_case 'Error: ERR_PNPM_MINIMUM_RELEASE_AGE_VIOLATION a@1.0.0\n' "[$human,$other]"
assert grep -qF 'api -X POST repos/o/r/issues/7/comments -F body=@' "$STUB_DIR/calls"
assert no_patch

# 8. Another failure: no gh call, exit 0.
run_case 'ERR_PNPM_FETCH_404 not found\n'
assert test "$status" -eq 0
assert no_calls

# 9. A missing log, for example when checkout failed: no gh call, exit 0.
STUB_DIR=$(mktemp -d "$work/case.XXXXXX")
export STUB_DIR
PATH="$work/bin:$PATH" REPO=o/r PR=7 bash "$here/release-age-comment.sh" "$work/none.log"
assert test "$?" -eq 0
assert no_calls

# 10. pnpm wraps its lines: right after "was published at", inside the cutoff, and a long
# scoped name with a build suffix.
run_case 'Error: ERR_PNPM_MINIMUM_RELEASE_AGE_VIOLATION
  ╰─▶ 2 lockfile entries failed verification:
        ignore@7.0.16 was published at
      2026-11-17T05:56:55.170Z, within the minimumReleaseAge
      cutoff (
      2026-11-16T23:05:09.237Z)
        @very-long-scope-name-for-testing/some-extremely-long-package-name@12.34.56-rc.1+build.5
      was published at 2026-11-17T06:00:00.000Z, within the
'
assert body_has "- \`ignore@7.0.16\`, published at \`2026-11-17T05:56:55.170Z\`"
assert body_has "- \`@very-long-scope-name-for-testing/some-extremely-long-package-name@12.34.56-rc.1+build.5\`, published at \`2026-11-17T06:00:00.000Z\`"
assert body_has "Cutoff of the failed run: \`2026-11-16T23:05:09.237Z\`"

# 11. Names may have capitals.
run_case 'Error: ERR_PNPM_MINIMUM_RELEASE_AGE_VIOLATION\n  JSONStream@1.3.6 was published at 2026-11-17T05:56:55.170Z, within the\n'
assert body_has "- \`JSONStream@1.3.6\`, published at"

# 12. The same entry twice is listed once.
run_case 'Error: ERR_PNPM_MINIMUM_RELEASE_AGE_VIOLATION
  dup@1.0.0 was published at 2026-11-17T05:56:55.170Z, within the
  dup@1.0.0 was published at 2026-11-17T05:56:55.170Z, within the
'
assert test "$(grep -c "^- \`dup@1.0.0\`" "$STUB_DIR/body")" -eq 1

# 13. Two cutoffs: the first one wins.
run_case 'Error: ERR_PNPM_MINIMUM_RELEASE_AGE_VIOLATION
  a@1.0.0 was published at 2026-11-17T05:56:55.170Z, within the minimumReleaseAge cutoff (2026-11-16T00:00:00.000Z)
  b@1.0.0 was published at 2026-11-17T05:56:55.170Z, within the minimumReleaseAge cutoff (2027-01-01T00:00:00.000Z)
'
assert body_has "\`2026-11-16T00:00:00.000Z\`"
assert body_lacks '2027-01-01'

# 14. A timestamp must be complete, and a cutoff must be a timestamp: nothing else is printed.
run_case 'Error: ERR_PNPM_MINIMUM_RELEASE_AGE_VIOLATION
  bad@1.0.0 was published at 2026-11-17T05:56:55.Z, within the
  worse@1.0.0 was published at 2026-11-17Tfoo, within the
  minimumReleaseAge cutoff (evil text)
'
assert body_lacks 'bad@1.0.0'
assert body_lacks 'worse@1.0.0'
assert body_lacks 'evil'
assert body_lacks 'Cutoff of the failed run'

# 15. Many entries: at most 50 are listed, then the rest is counted, and the body stays small.
big='Error: ERR_PNPM_MINIMUM_RELEASE_AGE_VIOLATION\n'
for i in $(seq 1 2000); do
  big+="  pkg$i@1.0.0 was published at 2026-11-17T05:56:55.170Z, within the\n  minimumReleaseAge cutoff (2026-11-16T23:05:09.237Z)\n"
done
run_case "$big"
assert test "$(grep -c "^- \`pkg" "$STUB_DIR/body")" -eq 50
assert body_has 'and 1950 more'
assert test "$(wc -c <"$STUB_DIR/body")" -lt 60000

# gen_log N: a log with N refused entries.
gen_log() {
  local out='Error: ERR_PNPM_MINIMUM_RELEASE_AGE_VIOLATION\n' i
  for ((i = 1; i <= $1; i++)); do
    out+="  pkg$i@1.0.0 was published at 2026-11-17T05:56:55.170Z, within the\n"
  done
  printf '%s' "$out"
}

# 15b. Exactly at the limit nothing is said about more, one above it the rest is counted.
run_case "$(gen_log 50)"
assert test "$(grep -c "^- \`pkg" "$STUB_DIR/body")" -eq 50
assert body_lacks ' more'
run_case "$(gen_log 51)"
assert test "$(grep -c "^- \`pkg" "$STUB_DIR/body")" -eq 50
assert body_has '- and 1 more'

# 15c. A package name is at most 214 characters long, the npm limit. A longer run of name
# characters is not a package and is not listed.
rep() { head -c "$1" /dev/zero | tr '\0' "$2"; }
run_case "Error: ERR_PNPM_MINIMUM_RELEASE_AGE_VIOLATION\n  $(rep 214 b)@1.0.0 was published at 2026-11-17T05:56:55.170Z, within the\n"
assert body_has "$(rep 214 b)@1.0.0"
run_case "Error: ERR_PNPM_MINIMUM_RELEASE_AGE_VIOLATION\n  $(rep 215 b)@1.0.0 was published at 2026-11-17T05:56:55.170Z, within the\n"
assert body_lacks "$(rep 20 b)"
run_case "Error: ERR_PNPM_MINIMUM_RELEASE_AGE_VIOLATION\n  $(rep 20000 a)@1.0.0 was published at 2026-11-17T05:56:55.170Z, within the\n"
assert body_lacks "$(rep 20 a)"
assert test "$(wc -c <"$STUB_DIR/body")" -lt 2000

# 16. The exit code of the install is preserved and its output is logged.
bash "$here/install-with-log.sh" "$work/install.log" sh -c 'echo installing; exit 7' >/dev/null
assert test "$?" -eq 7
assert grep -qF installing "$work/install.log"
bash "$here/install-with-log.sh" "$work/install.log" sh -c 'echo fine; echo oops >&2' >/dev/null
assert test "$?" -eq 0
assert grep -qF oops "$work/install.log"

# 17. A second run overwrites the log, it never appends to an old one.
printf 'OLD LOG\n' >"$work/old.log"
bash "$here/install-with-log.sh" "$work/old.log" sh -c 'echo new' >/dev/null
assert test "$(cat "$work/old.log")" = new

printf '%d checks, %d failed\n' "$checks" "$fails"
[[ $fails -eq 0 ]]
