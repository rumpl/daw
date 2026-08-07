#!/usr/bin/env bash
# OPT-IN real-agent smoke test.
#
# This script is NEVER run by `make test` or `make test-e2e`. It sends one real
# prompt to a real model through the real docker-agent runtime and therefore
# SPENDS MODEL TOKENS. It does not start a sandbox VM and always uses the
# dashboard's SDK-built coding agent.
#
# Usage:
#   scripts/smoke-real.sh /abs/path/to/workspace
#
set -euo pipefail

WORKSPACE="${1:-$PWD}"
PORT="${PORT:-4788}"
BASE="http://127.0.0.1:${PORT}"

read -r -p "This sends a REAL prompt to a REAL model and will spend tokens. Continue? [y/N] " ans
[[ "$ans" == "y" || "$ans" == "Y" ]] || { echo "aborted"; exit 1; }

echo "==> health"
curl -fsS "$BASE/api/health" && echo

echo "==> bootstrap"
TOKEN=$(curl -fsS "$BASE/api/bootstrap" | python3 -c 'import sys,json;print(json.load(sys.stdin)["csrfToken"])')

post() { curl -fsS -X POST "$BASE$1" -H "X-DAW-CSRF: $TOKEN" -H 'Content-Type: application/json' -d "$2"; }

echo "==> open workspace"
WSID=$(post /api/workspaces/open "{\"path\":\"$WORKSPACE\"}" | python3 -c 'import sys,json;print(json.load(sys.stdin)["workspaceId"])')

echo "==> create chat"
CHAT=$(post /api/chats "{\"workspaceId\":\"$WSID\"}")
CHATID=$(echo "$CHAT" | python3 -c 'import sys,json;print(json.load(sys.stdin)["chatId"])')
echo "$CHAT"

echo "==> send one prompt (this spends tokens)"
post "/api/chats/$CHATID/messages" '{"text":"Reply with the single word: pong","mode":"normal"}'
echo

echo "==> streaming for 60s (ctrl-c to stop)"
curl -N -fsS "$BASE/api/chats/$CHATID/events" &
CURL_PID=$!
sleep 60
kill "$CURL_PID" 2>/dev/null || true

echo "==> snapshot"
curl -fsS "$BASE/api/chats/$CHATID" | python3 -m json.tool | head -60

echo "==> dispose"
curl -fsS -X DELETE "$BASE/api/chats/$CHATID" -H "X-DAW-CSRF: $TOKEN"
echo
echo "Manual checklist:"
echo "  [ ] assistant text streamed"
echo "  [ ] a tool confirmation appeared and the pattern matched the dialog"
echo "  [ ] token/cost updated"
echo "  [ ] the session is visible in the sidebar after a page reload"
echo "  [ ] docker agent run --session <id> opens the same conversation"
