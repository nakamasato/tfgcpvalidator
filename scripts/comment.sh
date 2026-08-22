#!/usr/bin/env bash
# Posts, updates, or hides the pull request comment, and adds or removes the
# label, based on how many findings the run produced.
#
# Inputs (environment):
#   COMMENT   auto | true | false
#   LABEL     label name, or empty to leave labels alone
#   FINDINGS  path to the `--format json` output
#   MARKDOWN  path to the `--format markdown` output
#   GH_TOKEN  token used for every API call
set -euo pipefail

# Every marker-matching comment is one this action owns, so the marker doubles
# as the identity of the comment across runs.
MARKER='<!-- tfgcpvalidator -->'

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
  matches=$(gh api --paginate \
    "repos/$GITHUB_REPOSITORY/issues/$PR/comments" \
    --jq ".[] | select(.body | startswith(\"$MARKER\")) | \"\(.id) \(.node_id)\"")
  printf '%s' "${matches%%$'\n'*}"
}

build_body() {
  local body
  body="$MARKER
## tfgcpvalidator

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

add_label() {
  jq -n --arg label "$LABEL" '{labels: [$label]}' |
    gh api --method POST "repos/$GITHUB_REPOSITORY/issues/$PR/labels" --input - >/dev/null
}

remove_label() {
  local encoded
  encoded=$(jq -rn --arg label "$LABEL" '$label | @uri')
  # A label that was never applied is not an error worth reporting.
  gh api --method DELETE \
    "repos/$GITHUB_REPOSITORY/issues/$PR/labels/$encoded" >/dev/null 2>&1 || true
}

apply() {
  local count
  count=$(jq '.findings | length' "$FINDINGS") || return 1

  # errexit is off inside a function whose failure is handled by the caller, so
  # each call propagates its own failure explicitly.
  if [ "$count" -gt 0 ]; then
    if [ "$want_comment" = true ]; then post_comment || return 1; fi
    if [ -n "$LABEL" ]; then add_label || return 1; fi
  else
    if [ "$want_comment" = true ]; then hide_comment || return 1; fi
    if [ -n "$LABEL" ]; then remove_label || return 1; fi
  fi
}

case "$COMMENT" in
  false) want_comment=false ;;
  true) want_comment=true ;;
  auto)
    case "${GITHUB_EVENT_NAME:-}" in
      pull_request | pull_request_target) want_comment=true ;;
      *) want_comment=false ;;
    esac
    ;;
  *)
    echo "comment must be auto, true or false, got: $COMMENT" >&2
    exit 2
    ;;
esac

if [ "$want_comment" = false ] && [ -z "$LABEL" ]; then
  exit 0
fi

PR=$(pr_number 2>/dev/null || true)
if [ -z "$PR" ]; then
  warn "no pull request in this event, so no comment or label was written"
  exit 0
fi

# The findings themselves are already reported by the Report step, so failing
# here would only bury them — a token without pull-requests: write, which is
# what a fork's pull_request event gets, degrades to a warning.
apply || warn "could not write the comment or label; the token may lack pull-requests: write"
