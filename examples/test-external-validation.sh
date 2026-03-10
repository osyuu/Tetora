#!/usr/bin/env bash
# ============================================================
# 場景 7：Validation 規則驗證
# ============================================================
#
# 使用情境：
#   使用者建立 workflow 時，各種不合法的設定應該被拒絕。
#   驗證 Tetora 的 validation 能正確攔截錯誤。
#
# 測試子場景：
#   A) 缺少 externalUrl → 400
#   B) externalBody + externalRawBody 同時存在 → 400
#   C) 缺少 callbackKey → 400
#   D) 不支援的 callbackMode → 400
#   E) callbackAccumulate + single mode → 400
#   F) callbackTimeout < 1s → 400
#   G) callbackTimeout > 30d → 400
#   H) open auth 沒有 {{runId}} → 400
#   I) streaming 沒有 donePath → 400
#   J) 合法 workflow → 201
#
# 覆蓋 spec 場景：
#   - Validation 規則全部（spec L1222-1322）
#   - R3: SaaS 用戶註冊驗證（合法 single callback + ResponseMapping）
#   - R10: 審計合規 error propagation（onTimeout:stop）
# ============================================================

set -euo pipefail

BASE="${1:-http://localhost:7200}"
TOKEN="${TETORA_API_TOKEN:-$(jq -r '.apiToken // empty' ~/.tetora/config.json 2>/dev/null || true)}"
AUTH_HEADER=""
if [ -n "$TOKEN" ]; then
  AUTH_HEADER="Authorization: Bearer $TOKEN"
fi

header_args=(-H "Content-Type: application/json")
if [ -n "$AUTH_HEADER" ]; then
  header_args+=(-H "$AUTH_HEADER")
fi

PASS=0
FAIL=0

check_validation() {
  local label="$1"
  local expected_code="$2"
  local data="$3"

  RESULT=$(curl -s -w "\n%{http_code}" -X POST "$BASE/workflows" \
    "${header_args[@]}" \
    -d "$data")
  HTTP_CODE=$(echo "$RESULT" | tail -1)
  BODY=$(echo "$RESULT" | sed '$d')

  if [ "$HTTP_CODE" = "$expected_code" ]; then
    echo "  ✓ $label → $HTTP_CODE"
    PASS=$((PASS + 1))
  else
    echo "  ✗ $label → got $HTTP_CODE, expected $expected_code"
    echo "    body: $BODY"
    FAIL=$((FAIL + 1))
  fi
}

echo "=== 場景 7：Validation 規則 ==="
echo ""

# A) 缺少 externalUrl
check_validation "A) 缺少 externalUrl" "400" '{
  "name": "test-val-a",
  "steps": [{"id": "s1", "type": "external", "callbackKey": "k-{{runId}}", "callbackTimeout": "1m"}]
}'

# B) externalBody + externalRawBody 同時
check_validation "B) body + rawBody 同時" "400" '{
  "name": "test-val-b",
  "steps": [{"id": "s1", "type": "external", "externalUrl": "https://example.com/api",
    "externalBody": {"a": "1"}, "externalRawBody": "<xml/>",
    "callbackKey": "k-{{runId}}", "callbackTimeout": "1m"}]
}'

# C) 缺少 callbackKey
check_validation "C) 缺少 callbackKey" "400" '{
  "name": "test-val-c",
  "steps": [{"id": "s1", "type": "external", "externalUrl": "https://example.com/api", "callbackTimeout": "1m"}]
}'

# D) 不支援的 callbackMode
check_validation "D) 不支援的 callbackMode" "400" '{
  "name": "test-val-d",
  "steps": [{"id": "s1", "type": "external", "externalUrl": "https://example.com/api",
    "callbackKey": "k-{{runId}}", "callbackMode": "batch", "callbackTimeout": "1m"}]
}'

# E) callbackAccumulate + single mode
check_validation "E) accumulate + single" "400" '{
  "name": "test-val-e",
  "steps": [{"id": "s1", "type": "external", "externalUrl": "https://example.com/api",
    "callbackKey": "k-{{runId}}", "callbackAccumulate": true, "callbackTimeout": "1m"}]
}'

# F) callbackTimeout < 1s
check_validation "F) timeout < 1s" "400" '{
  "name": "test-val-f",
  "steps": [{"id": "s1", "type": "external", "externalUrl": "https://example.com/api",
    "callbackKey": "k-{{runId}}", "callbackTimeout": "500ms"}]
}'

# G) callbackTimeout > 30d
check_validation "G) timeout > 30d" "400" '{
  "name": "test-val-g",
  "steps": [{"id": "s1", "type": "external", "externalUrl": "https://example.com/api",
    "callbackKey": "k-{{runId}}", "callbackTimeout": "31d"}]
}'

# H) open auth 沒有 {{runId}}
check_validation "H) open auth 無 runId" "400" '{
  "name": "test-val-h",
  "steps": [{"id": "s1", "type": "external", "externalUrl": "https://example.com/api",
    "callbackKey": "static-key", "callbackAuth": "open", "callbackTimeout": "1m"}]
}'

# I) streaming 沒有 donePath
check_validation "I) streaming 無 donePath" "400" '{
  "name": "test-val-i",
  "steps": [{"id": "s1", "type": "external", "externalUrl": "https://example.com/api",
    "callbackKey": "k-{{runId}}", "callbackMode": "streaming", "callbackTimeout": "1m",
    "callbackResponseMap": {"dataPath": "data"}}]
}'

# J) 合法 workflow → 201
check_validation "J) 合法 workflow" "201" '{
  "name": "test-val-valid",
  "steps": [{"id": "s1", "type": "external", "externalUrl": "https://example.com/api",
    "callbackKey": "k-{{runId}}", "callbackTimeout": "5m", "callbackAuth": "open", "onTimeout": "stop"}]
}'

# Cleanup
curl -s -X DELETE "$BASE/workflows/test-val-valid" "${header_args[@]}" > /dev/null 2>&1 || true

echo ""
echo "  結果：$PASS passed, $FAIL failed"
echo ""
echo "=== 場景 7 完成 ==="

if [ "$FAIL" -gt 0 ]; then exit 1; fi
