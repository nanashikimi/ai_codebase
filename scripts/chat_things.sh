#!/usr/bin/env bash
set -euo pipefail

BASE_URL="http://localhost:8081"

ask() {
  local q="$1"
  echo "=== QUESTION: $q"
  curl -s "$BASE_URL/chat" \
    -X POST \
    -H "Content-Type: application/json" \
    -d "{\"question\":\"$q\",\"model\":\"qwen2.5:3b\"}"
  echo
  echo
}

ask "Where is the HTTP server started?"
ask "Where is /chat handled?"
ask "Where are HTTP handlers registered?"
ask "Where is search_code implemented?"
ask "Where is open_file implemented?"
