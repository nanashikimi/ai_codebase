#!/usr/bin/env bash
set -euo pipefail

BASE_URL="http://localhost:8081"
FAIL=0

run_test() {
  local question="$1"
  local expected="$2"

  echo "=== TEST: $question"
  resp=$(curl -s "$BASE_URL/chat" \
    -X POST \
    -H "Content-Type: application/json" \
    -d "{\"question\":\"$question\",\"model\":\"qwen2.5:3b\"}")

  echo "$resp"

  if [[ "$resp" != *"$expected"* ]]; then
    echo "FAIL: expected substring '$expected'"
    FAIL=1
  else
    echo "PASS"
  fi

  echo
}

run_test "Where is the HTTP server started?" "cmd/mcp-server/main.go:87"
run_test "Where is /chat handled?" "cmd/mcp-server/main.go:59"
run_test "Where is search_code implemented?" "internal/tools/search_code.go"
run_test "Where is open_file implemented?" "internal/tools/open_file.go"

exit $FAIL
