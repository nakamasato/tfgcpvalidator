#!/usr/bin/env bash
# Posts, updates, or hides the pull request comment based on how many findings
# the run produced.
#
# Inputs (environment):
#   COMMENT   auto | true | false
#   TARGET    name distinguishing one call from another, or empty
#   FINDINGS  path to the `--format json` output
#   MARKDOWN  path to the `--format markdown` output
#   GH_TOKEN  token used for every API call
set -euo pipefail

# GitHub rejects an issue comment body over 65536 characters. The margin covers
# the header, the marker, and the truncation notice appended after the cut.
BODY_LIMIT=65000

warn() {
  echo "::warning title=tfgcpvalidator::$1"
}

pr_number() {
  jq -er '.pull_request.number // .issue.number // empty' "${GITHUB_EVENT_PATH:-}"
}

# Prints "<id> <node_id>" of the existing comment, or nothing. The pages are
# collected before the first match is taken so that closing the pipe early
# cannot kill gh mid-pagination.
find_comment() {
  local matches
  matches=$(
    gh api --paginate "repos/$GITHUB_REPOSITORY/issues/$PR/comments" \
      --jq '.[] | {id, node_id, body}' |
      jq -r --arg marker "$MARKER" \
        'select(.body | startswith($marker)) | "\(.id) \(.node_id)"'
  )
  printf '%s' "${matches%%$'\n'*}"
}

build_body() {
  local body
  body="$MARKER
## GCP Validation$TITLE_SUFFIX

$(cat "$MARKDOWN")"

  if [ "${#body}" -le "$BODY_LIMIT" ]; then
    printf '%s\n' "$body"
    return
  fi

  # Cutting on a character boundary would leave a half-written table row, so the
  # body is trimmed whole lines at a time until it fits.
  local trimmed='' line
  while IFS= read -r line; do
    if [ $((${#trimmed} + ${#line} + 1)) -gt "$BODY_LIMIT" ]; then
      break
    fi
    trimmed="$trimmed$line
"
  done <<<"$body"

  printf '%s\n_Report truncated: it exceeded the maximum comment length._\n' "$trimmed"
}

write_comment() {
  jq -n --arg body "$(build_body)" '{body: $body}' |
    gh api --method "$1" "$2" --input - >/dev/null
}

is_minimized() {
  # shellcheck disable=SC2016  # $id is a GraphQL variable, not a shell one
  gh api graphql -F id="$1" -f query='
    query($id: ID!) {
      node(id: $id) { ... on IssueComment { isMinimized } }
    }' --jq '.data.node.isMinimized'
}

# GitHub keeps a comment collapsed after an edit, so a comment hidden by an
# earlier clean run has to be reopened before the new report is readable.
# unminimizeComment rejects a comment that is not minimized, hence the check.
unminimize_comment() {
  [ "$(is_minimized "$1")" = true ] || return 0
  # shellcheck disable=SC2016
  gh api graphql -F id="$1" -f query='
    mutation($id: ID!) {
      unminimizeComment(input: {subjectId: $id}) { clientMutationId }
    }' >/dev/null
}

minimize_comment() {
  [ "$(is_minimized "$1")" = false ] || return 0
  # shellcheck disable=SC2016
  gh api graphql -F id="$1" -f query='
    mutation($id: ID!) {
      minimizeComment(input: {subjectId: $id, classifier: OUTDATED}) { clientMutationId }
    }' >/dev/null
}

post_comment() {
  local existing id node_id
  existing=$(find_comment) || return 1

  if [ -z "$existing" ]; then
    write_comment POST "repos/$GITHUB_REPOSITORY/issues/$PR/comments" || return 1
    return 0
  fi

  read -r id node_id <<<"$existing"
  write_comment PATCH "repos/$GITHUB_REPOSITORY/issues/comments/$id" || return 1
  unminimize_comment "$node_id" || return 1
}

hide_comment() {
  local existing id node_id
  existing=$(find_comment) || return 1
  [ -n "$existing" ] || return 0

  read -r id node_id <<<"$existing"
  minimize_comment "$node_id" || return 1
}

apply() {
  local count
  count=$(jq '.findings | length' "$FINDINGS") || return 1

  # errexit is off inside a function whose failure is handled by the caller, so
  # each call propagates its own failure explicitly.
  if [ "$count" -gt 0 ]; then
    post_comment || return 1
  else
    hide_comment || return 1
  fi
}

case "$COMMENT" in
  false) exit 0 ;;
  true) ;;
  auto)
    case "${GITHUB_EVENT_NAME:-}" in
      pull_request | pull_request_target) ;;
      *) exit 0 ;;
    esac
    ;;
  *)
    echo "comment must be auto, true or false, got: $COMMENT" >&2
    exit 2
    ;;
esac

# The target lands inside an HTML comment and inside a jq string, so anything
# that could close the marker early or escape the string is refused outright.
if [ -n "$TARGET" ] && ! [[ $TARGET =~ ^[A-Za-z0-9._/-]+$ ]]; then
  echo "target may contain only letters, digits, and . _ / -, got: $TARGET" >&2
  exit 2
fi

# The marker is the identity of the comment across runs, and the target is what
# keeps two calls on one pull request from overwriting each other.
if [ -n "$TARGET" ]; then
  MARKER="<!-- tfgcpvalidator:$TARGET -->"
  TITLE_SUFFIX=" ($TARGET)"
else
  MARKER='<!-- tfgcpvalidator -->'
  TITLE_SUFFIX=''
fi

PR=$(pr_number 2>/dev/null || true)
if [ -z "$PR" ]; then
  warn "no pull request in this event, so no comment was written"
  exit 0
fi

# The findings themselves are already reported by the Report step, so failing
# here would only bury them — a token without pull-requests: write, which is
# what a fork's pull_request event gets, degrades to a warning.
apply || warn "could not write the comment; the token may lack pull-requests: write"
