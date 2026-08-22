#!/usr/bin/env bash
# Runs comment.sh against a stubbed `gh` and asserts which API calls it made.
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
work=$(mktemp -d "${TMPDIR:-/tmp}/tfgcpvalidator-test.XXXXXX")
trap 'rm -rf "$work"' EXIT

mkdir -p "$work/bin"
cat >"$work/bin/gh" <<'STUB'
#!/usr/bin/env bash
# Records the call and answers from the fixture the case selected.
set -euo pipefail
printf '%s\n' "$*" >>"$GH_CALLS"

# Exiting without draining the body would send SIGPIPE to the jq that writes
# it, which pipefail then reports as a failed call.
case "$*" in
  *"--input -"*) cat >/dev/null ;;
esac

if [ "${GH_FAIL:-}" = 1 ]; then
  echo "stub: forced failure" >&2
  exit 1
fi

case "$*" in
  *"issues/7/comments"*)
    if [ "${GH_EXISTING:-}" = 1 ]; then
      printf '%s\n' "42 IC_node42"
    fi
    ;;
  *isMinimized*)
    printf '%s\n' "${GH_MINIMIZED:-false}"
    ;;
esac
STUB
chmod +x "$work/bin/gh"
PATH="$work/bin:$PATH"

cat >"$work/event.json" <<'JSON'
{"pull_request": {"number": 7}}
JSON

printf '%s\n' '{"findings":[{"address":"a"}],"error_count":1,"warn_count":0}' >"$work/some.json"
printf '%s\n' '{"findings":[],"error_count":0,"warn_count":0}' >"$work/none.json"
printf '%s\n' '| Severity | Resource |' >"$work/report.md"

failures=0

# run <name> <expected-call-substrings...>; fixture comes from the environment.
run() {
  local name=$1 status=0
  shift
  export GH_CALLS="$work/calls.txt"
  : >"$GH_CALLS"

  local out
  out=$(
    GITHUB_REPOSITORY=o/r \
      GITHUB_EVENT_PATH="${TEST_EVENT:-$work/event.json}" \
      MARKDOWN="$work/report.md" \
      GH_TOKEN=x \
      bash "$here/comment.sh" 2>&1
  ) || status=$?

  local calls
  calls=$(cat "$GH_CALLS")

  # A failure inside the script degrades to a warning and still exits 0, so
  # without this a broken case would look like a passing one.
  if [ "${WANT_WARN:-0}" = 0 ] && grep -q '::warning' <<<"$out"; then
    echo "FAIL $name: warned unexpectedly"
    echo "  output: $out"
    failures=$((failures + 1))
    return
  fi

  if [ "$status" -ne "${WANT_STATUS:-0}" ]; then
    echo "FAIL $name: exit $status, want ${WANT_STATUS:-0}"
    echo "  output: $out"
    failures=$((failures + 1))
    return
  fi

  local want
  for want in "$@"; do
    case "$want" in
      !*)
        if grep -qF -- "${want#!}" <<<"$calls"; then
          echo "FAIL $name: called '${want#!}' but should not have"
          echo "  calls: $calls"
          failures=$((failures + 1))
        fi
        ;;
      *)
        if ! grep -qF -- "$want" <<<"$calls"; then
          echo "FAIL $name: no call matching '$want'"
          echo "  calls: $calls"
          failures=$((failures + 1))
        fi
        ;;
    esac
  done
}

export COMMENT=auto LABEL='' FINDINGS="$work/some.json"
export GITHUB_EVENT_NAME=push
run "auto on push does nothing" '!api'

export GITHUB_EVENT_NAME=pull_request
run "findings with no existing comment posts one" \
  '--method POST repos/o/r/issues/7/comments' '!PATCH'

GH_EXISTING=1 run "findings with an existing comment update it" \
  '--method PATCH repos/o/r/issues/comments/42' 'isMinimized' '!unminimizeComment'

GH_EXISTING=1 GH_MINIMIZED=true run "an update reopens a hidden comment" \
  '--method PATCH repos/o/r/issues/comments/42' 'unminimizeComment'

FINDINGS="$work/none.json" GH_EXISTING=1 run "no findings hides the comment" \
  'minimizeComment' '!--method POST' '!--method PATCH'

FINDINGS="$work/none.json" GH_EXISTING=1 GH_MINIMIZED=true \
  run "an already hidden comment stays as is" '!minimizeComment'

FINDINGS="$work/none.json" run "no findings and no comment writes nothing" \
  '!minimizeComment' '!--method'

COMMENT=false LABEL=needs-fix run "a label is added while there are findings" \
  '--method POST repos/o/r/issues/7/labels' '!issues/7/comments'

COMMENT=false LABEL='needs fix' FINDINGS="$work/none.json" \
  run "the label is removed once there are none" \
  '--method DELETE repos/o/r/issues/7/labels/needs%20fix'

COMMENT=false LABEL='' run "nothing to do exits early" '!api'

GH_FAIL=1 WANT_WARN=1 run "an API failure warns instead of failing the job" \
  'issues/7/comments'

TEST_EVENT=/nonexistent WANT_WARN=1 run "an event without a pull request is skipped" '!api'

COMMENT=bogus WANT_STATUS=2 run "an invalid comment input is rejected" '!api'

if [ "$failures" -gt 0 ]; then
  echo "$failures failure(s)"
  exit 1
fi
echo "all cases passed"
