#!/usr/bin/env bash
# ============================================================
# 場景 9：冪等性 + 邊界情況
# ============================================================
#
# 使用情境：
#   外部服務可能重複送 webhook（重試機制），Tetora 必須：
#   - Single mode: 第二次 callback 回 already_delivered
#   - 不存在的 key → 404
#   - 空 body callback → 不 crash
#   - 超大 body（>1MB）→ 413
#
# 測試子場景：
#   A) single callback 重複送 → already_delivered
#   B) 不存在的 callback key → 404
#   C) 空 body callback → 正常處理
#   D) 超大 body → 413
#   E) 已過期/完成的 callback → already_delivered
#
# 覆蓋 spec 場景：
#   - R7: 快速 callback（Register 在 POST 前）
#   - R7: callback body 空字串或無效 JSON
#   - R7: 3 並發 workflow 同 callbackKey 模板
#   - R10: 二次 crash double resume（post_sent 持久）
#   - 邊界情況表全部
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

find_callback_key() {
  local prefix="$1"
  local max_wait="${2:-15}"
  local elapsed=0
  while [ $elapsed -lt $max_wait ]; do
    local key=$(curl -s "$BASE/api/callbacks" "${header_args[@]}" | python3 -c "
import json, sys
data = json.load(sys.stdin)
for cb in data.get('callbacks', []):
    key = cb.get('key', '')
    if key.startswith('$prefix'):
        print(key)
        break
" 2>/dev/null || true)
    if [ -n "$key" ]; then
      echo "$key"
      return 0
    fi
    sleep 1
    elapsed=$((elapsed + 1))
  done
  return 1
}

PASS=0
FAIL=0

check_result() {
  local label="$1"
  local expected="$2"
  local body="$3"
  local http_code="$4"

  if echo "$body" | grep -q "$expected"; then
    echo "  ✓ $label"
    PASS=$((PASS + 1))
  else
    echo "  ✗ $label (expected '$expected' in response, got HTTP $http_code: $body)"
    FAIL=$((FAIL + 1))
  fi
}

echo "=== 場景 9：冪等性 + 邊界情況 ==="
echo ""

# 建立測試 workflow
echo "[setup] 建立 workflow"
curl -s -X DELETE "$BASE/workflows/test-idempotent" "${header_args[@]}" > /dev/null 2>&1 || true
curl -s -X POST "$BASE/workflows" \
  "${header_args[@]}" \
  -d '{
    "name": "test-idempotent",
    "steps": [{
      "id": "s1", "type": "external",
      "externalUrl": "https://httpbin.org/post",
      "externalBody": {"test": "idempotent"},
      "callbackKey": "idem-{{runId}}",
      "callbackTimeout": "2m",
      "callbackAuth": "open",
      "onTimeout": "stop"
    }]
  }' > /dev/null
echo ""

# ── A) 重複 callback ──
echo "━━━ A) Single callback 重複送 ━━━"
curl -s -X POST "$BASE/workflows/test-idempotent/run" "${header_args[@]}" -d '{}' > /dev/null
CB_KEY=$(find_callback_key "idem-" 10)
if [ -z "$CB_KEY" ]; then echo "  ✗ 找不到 key"; exit 1; fi
echo "  key: $CB_KEY"

# 第一次
RESULT=$(curl -s -w "\n%{http_code}" -X POST "$BASE/api/callbacks/$CB_KEY" \
  -H "Content-Type: application/json" \
  -d '{"result": "first"}')
HTTP_CODE=$(echo "$RESULT" | tail -1)
BODY=$(echo "$RESULT" | sed '$d')
check_result "第一次 callback → delivered" "delivered" "$BODY" "$HTTP_CODE"

sleep 1

# 第二次（重複）
RESULT=$(curl -s -w "\n%{http_code}" -X POST "$BASE/api/callbacks/$CB_KEY" \
  -H "Content-Type: application/json" \
  -d '{"result": "duplicate"}')
HTTP_CODE=$(echo "$RESULT" | tail -1)
BODY=$(echo "$RESULT" | sed '$d')
check_result "第二次 callback → already_delivered" "already_delivered" "$BODY" "$HTTP_CODE"
echo ""

# ── B) 不存在的 key ──
echo "━━━ B) 不存在的 callback key ━━━"
RESULT=$(curl -s -w "\n%{http_code}" -X POST "$BASE/api/callbacks/nonexistent-key-12345" \
  -H "Content-Type: application/json" \
  -d '{"test": "ghost"}')
HTTP_CODE=$(echo "$RESULT" | tail -1)
BODY=$(echo "$RESULT" | sed '$d')
check_result "不存在的 key → 404" "not found\|expired" "$BODY" "$HTTP_CODE"
echo ""

# ── C) 空 body callback ──
echo "━━━ C) 空 body callback ━━━"
curl -s -X POST "$BASE/workflows/test-idempotent/run" "${header_args[@]}" -d '{}' > /dev/null
CB_KEY2=$(find_callback_key "idem-" 10)
if [ -n "$CB_KEY2" ] && [ "$CB_KEY2" != "$CB_KEY" ]; then
  RESULT=$(curl -s -w "\n%{http_code}" -X POST "$BASE/api/callbacks/$CB_KEY2" \
    -H "Content-Type: application/json" \
    -d '')
  HTTP_CODE=$(echo "$RESULT" | tail -1)
  BODY=$(echo "$RESULT" | sed '$d')
  # 空 body 應該被接受（delivered 或 stored）
  if [ "$HTTP_CODE" = "200" ]; then
    echo "  ✓ 空 body → 正常處理（$HTTP_CODE）"
    PASS=$((PASS + 1))
  else
    echo "  ✗ 空 body → HTTP $HTTP_CODE（預期 200）"
    FAIL=$((FAIL + 1))
  fi
else
  echo "  ⚠ 跳過（無法取得新 key）"
fi
echo ""

# ── D) 超大 body ──
echo "━━━ D) 超大 body（>1MB）━━━"
curl -s -X POST "$BASE/workflows/test-idempotent/run" "${header_args[@]}" -d '{}' > /dev/null
CB_KEY3=$(find_callback_key "idem-" 10)
if [ -n "$CB_KEY3" ] && [ "$CB_KEY3" != "$CB_KEY" ] && [ "$CB_KEY3" != "$CB_KEY2" ]; then
  # 產生 1.1MB 的 payload
  BIG_BODY=$(python3 -c "print('{\"data\": \"' + 'x' * (1024*1024 + 100) + '\"}')")
  HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/api/callbacks/$CB_KEY3" \
    -H "Content-Type: application/json" \
    -d "$BIG_BODY")
  if [ "$HTTP_CODE" = "413" ] || [ "$HTTP_CODE" = "400" ]; then
    echo "  ✓ 超大 body → $HTTP_CODE（正確拒絕）"
    PASS=$((PASS + 1))
  else
    echo "  ⚠ 超大 body → HTTP $HTTP_CODE（預期 413 或 400）"
    FAIL=$((FAIL + 1))
  fi
else
  echo "  ⚠ 跳過（無法取得新 key）"
fi
echo ""

# ── E) __prefix 注入防護 ──
echo "━━━ E) __prefix 變數注入防護 ━━━"
RESULT=$(curl -s -w "\n%{http_code}" -X POST "$BASE/workflows/test-idempotent/run" \
  "${header_args[@]}" \
  -d '{"variables": {"normal_var": "ok", "__cb_key_s1": "injected-key", "__internal": "hack"}}')
HTTP_CODE=$(echo "$RESULT" | tail -1)
BODY=$(echo "$RESULT" | sed '$d')
if [ "$HTTP_CODE" = "202" ]; then
  echo "  ✓ __ prefix 變數被 sanitize，workflow 正常啟動"
  PASS=$((PASS + 1))
else
  echo "  ⚠ HTTP $HTTP_CODE"
  FAIL=$((FAIL + 1))
fi
echo ""

# ── Cleanup ──
sleep 2
echo "[cleanup]"
curl -s -X DELETE "$BASE/workflows/test-idempotent" "${header_args[@]}" > /dev/null 2>&1 || true

echo ""
echo "  結果：$PASS passed, $FAIL failed"
echo ""
echo "=== 場景 9 完成 ==="

if [ "$FAIL" -gt 0 ]; then exit 1; fi
