#!/usr/bin/env bash
#
# autotest.sh — wisp's one-command test gauntlet.
#
# This is the single command the Claude Code auto-test loop runs each iteration.
# (CI runs the same steps as separate workflow jobs rather than this script.)
# It exercises wisp from the inside out:
#
#   1. gofmt    — formatting is clean (CI rejects unformatted code)
#   2. go vet   — static checks pass
#   3. go test -race ./...   — unit + in-process integration tests, race detector on
#   4. go build ./...        — the default (pure-Go) build compiles
#   5. go test -tags e2e ./internal/e2e/   — BLACK-BOX tests of the real binary:
#                                            it compiles wisp, attaches a PTY,
#                                            connects to an in-process SSH server,
#                                            types input, and asserts on the
#                                            rendered terminal output.
#
# Step 5 is the part that proves "wisp works as a terminal" rather than just
# "wisp's packages have correct unit behavior". If tailnet credentials are
# present in the environment (WISP_E2E_TS_CLIENT_SECRET / WISP_E2E_TS_TAGS /
# WISP_E2E_HOST / WISP_E2E_USER, plus WISP_E2E_SSH_KEY or WISP_E2E_PASSWORD),
# step 5 additionally runs a live test through the real tsnet path (authenticated
# by an OAuth client secret, not a long-lived key); otherwise that test skips.
#
# Usage:
#   scripts/autotest.sh              run the gauntlet once; exit non-zero on first failure
#   scripts/autotest.sh --loop       repeat until a step fails (surfaces flakiness / race)
#   scripts/autotest.sh --loop N     repeat at most N times, stopping early on failure
#   scripts/autotest.sh --quick      skip the e2e step (fast inner-loop while iterating)
#
# Designed so a single non-zero exit is an unambiguous "wisp is broken, fix it"
# signal for the loop driving this script.

set -uo pipefail

cd "$(dirname "$0")/.." || { echo "autotest: cannot cd to repo root" >&2; exit 1; }

LOOP=0
LOOP_MAX=0   # 0 == unbounded
QUICK=0

while [ $# -gt 0 ]; do
  case "$1" in
    --loop)
      LOOP=1
      if [[ "${2:-}" =~ ^[0-9]+$ ]]; then LOOP_MAX="$2"; shift; fi
      ;;
    --quick) QUICK=1 ;;
    -h|--help)
      sed -n '2,40p' "$0"; exit 0 ;;
    *)
      echo "autotest: unknown argument: $1" >&2; exit 2 ;;
  esac
  shift
done

# --- step runner -------------------------------------------------------------

step() { # step "name" cmd...
  local name="$1"; shift
  printf '\n\033[1m==> %s\033[0m\n' "$name"
  if "$@"; then
    printf '\033[32m    ok: %s\033[0m\n' "$name"
    return 0
  fi
  printf '\033[31m    FAIL: %s\033[0m\n' "$name"
  return 1
}

gofmt_check() {
  local unformatted
  unformatted="$(gofmt -l .)"
  if [ -n "$unformatted" ]; then
    echo "gofmt needs to run on:" >&2
    echo "$unformatted" >&2
    return 1
  fi
}

run_once() {
  step "gofmt"           gofmt_check                || return 1
  step "go vet"          go vet ./...               || return 1
  step "unit+race"       go test -race ./...        || return 1
  step "build (pure Go)" go build ./...             || return 1
  if [ "$QUICK" -eq 0 ]; then
    step "e2e (real binary)" go test -tags e2e -count=1 ./internal/e2e/... || return 1
  else
    echo "    (skipping e2e: --quick)"
  fi
}

# --- drive -------------------------------------------------------------------

if [ "$LOOP" -eq 0 ]; then
  if run_once; then
    printf '\n\033[32mALL GREEN — wisp works.\033[0m\n'
    exit 0
  fi
  printf '\n\033[31mGAUNTLET FAILED — see the FAIL step above.\033[0m\n'
  exit 1
fi

iter=0
while true; do
  iter=$((iter + 1))
  printf '\n\033[1m######## autotest iteration %d ########\033[0m\n' "$iter"
  if ! run_once; then
    printf '\n\033[31mFAILED on iteration %d.\033[0m\n' "$iter"
    exit 1
  fi
  printf '\n\033[32miteration %d green.\033[0m\n' "$iter"
  if [ "$LOOP_MAX" -gt 0 ] && [ "$iter" -ge "$LOOP_MAX" ]; then
    printf '\n\033[32mAll %d iterations green — wisp works.\033[0m\n' "$iter"
    exit 0
  fi
done
