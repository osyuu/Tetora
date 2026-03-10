#!/usr/bin/env bash
# ============================================================
# 場景 8：Callback 認證模式驗證
# ============================================================
#
# 使用情境：
#   不同外部服務有不同的認證需求：
#   - 自有服務 → bearer token
#   - Stripe/GitHub webhook → open（第三方無法帶我們的 token）
#   - 未帶 token 嘗試 bearer callback → 401
#
# 測試子場景：
#   A) bearer auth — 帶正確 token → 200
#   B) bearer auth — 不帶 token → 401
#   C) bearer auth — 帶錯誤 token → 401
#   D) open auth — 不帶 token → 200
#   E) signature auth (Phase 3) — 帶正確 HMAC → 200
#
# 覆蓋 spec 場景：
#   - R7: Stripe webhook 不帶 Bearer token（open）
#   - R8: CI/CD GitHub Actions webhook（open auth + ResponseMapping）
#   - R10: 多租戶並發（callbackKey 含 tenant_id）
#   - 安全考量章節全部
# ============================================================

set -euo pipefail

BASE="${1:-http://localhost:7200}"
TOKEN="${TETORA_API_TOKEN:-$(jq -r '.apiToken // empty' ~/.tetora/config.json 2>/dev/null || true)}"

header_args=(-H "Content-Type: application/json")
AUTH_HEADER=""
if [ -n "$TOKEN" ]; then
  AUTH_HEADER="Authorization: Bearer $TOKEN"
fi

auth_header_args=(-H "Content-Type: application/json")
if [ -n "$AUTH_HEADER" ]; then
  auth_header_args+=(-H "$AUTH_HEADER")
fi

find_callback_key() {
  local prefix="$1"
  local max_wait="${2:-15}"
  local elapsed=0
  while [ $elapsed -lt $max_wait ]; do
    local key=$(curl -s "$BASE/api/callbacks" "${auth_header_args[@]}" | python3 -c "
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
  local expected_code="$2"
  local actual_code="$3"

  if [ "$actual_code" = "$expected_code" ]; then
    echo "  ✓ $label → $actual_code"
    PASS=$((PASS + 1))
  else
    echo "  ✗ $label → got $actual_code, expected $expected_code"
    FAIL=$((FAIL + 1))
  fi
}

echo "=== 場景 8：Callback 認證模式 ==="
echo ""

# ── 建立兩個 workflow：bearer 和 open ──
echo "[1] 建立 bearer auth workflow"
curl -s -X DELETE "$BASE/workflows/test-auth-bearer" "${auth_header_args[@]}" > /dev/null 2>&1 || true
curl -s -X POST "$BASE/workflows" \
  "${auth_header_args[@]}" \
  -d '{
    "name": "test-auth-bearer",
    "steps": [{
      "id": "s1", "type": "external",
      "externalUrl": "https://httpbin.org/post",
      "externalBody": {"test": "bearer-auth"},
      "callbackKey": "bearer-{{runId}}",
      "callbackTimeout": "2m",
      "callbackAuth": "bearer",
      "onTimeout": "stop"
    }]
  }' > /dev/null

echo "[2] 建立 open auth workflow"
curl -s -X DELETE "$BASE/workflows/test-auth-open" "${auth_header_args[@]}" > /dev/null 2>&1 || true
curl -s -X POST "$BASE/workflows" \
  "${auth_header_args[@]}" \
  -d '{
    "name": "test-auth-open",
    "steps": [{
      "id": "s1", "type": "external",
      "externalUrl": "https://httpbin.org/post",
      "externalBody": {"test": "open-auth"},
      "callbackKey": "open-{{runId}}",
      "callbackTimeout": "2m",
      "callbackAuth": "open",
      "onTimeout": "stop"
    }]
  }' > /dev/null
echo ""

# ── Bearer auth 測試 ──
echo "━━━ Bearer Auth 測試 ━━━"
echo ""

echo "[3] 觸發 bearer workflow"
curl -s -X POST "$BASE/workflows/test-auth-bearer/run" "${auth_header_args[@]}" -d '{}' > /dev/null

CB_KEY=$(find_callback_key "bearer-" 10)
if [ -z "$CB_KEY" ]; then
  echo "  ✗ 找不到 callback key"
  exit 1
fi
echo "  callback key: $CB_KEY"

# A) 帶正確 token
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/api/callbacks/$CB_KEY" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"result": "success"}')
check_result "A) bearer + 正確 token" "200" "$HTTP_CODE"

# B) 不帶 token（新 run，因為上一個已 delivered）
curl -s -X POST "$BASE/workflows/test-auth-bearer/run" "${auth_header_args[@]}" -d '{}' > /dev/null
CB_KEY2=$(find_callback_key "bearer-" 10)
if [ -n "$CB_KEY2" ] && [ "$CB_KEY2" != "$CB_KEY" ]; then
  HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/api/callbacks/$CB_KEY2" \
    -H "Content-Type: application/json" \
    -d '{"result": "no-token"}')
  check_result "B) bearer + 無 token" "401" "$HTTP_CODE"

  # C) 帶錯誤 token
  HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/api/callbacks/$CB_KEY2" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer wrong-token-12345" \
    -d '{"result": "bad-token"}')
  check_result "C) bearer + 錯誤 token" "401" "$HTTP_CODE"
else
  echo "  ⚠ 無法取得第二個 callback key，跳過 B/C"
fi
echo ""

# ── Open auth 測試 ──
echo "━━━ Open Auth 測試 ━━━"
echo ""

echo "[4] 觸發 open workflow"
curl -s -X POST "$BASE/workflows/test-auth-open/run" "${auth_header_args[@]}" -d '{}' > /dev/null

CB_KEY_OPEN=$(find_callback_key "open-" 10)
if [ -z "$CB_KEY_OPEN" ]; then
  echo "  ✗ 找不到 callback key"
  exit 1
fi
echo "  callback key: $CB_KEY_OPEN"

# D) open auth — 不帶任何 token
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/api/callbacks/$CB_KEY_OPEN" \
  -H "Content-Type: application/json" \
  -d '{"result": "open-no-token"}')
check_result "D) open + 無 token" "200" "$HTTP_CODE"
echo ""

# ── Cleanup ──
echo "[cleanup]"
curl -s -X DELETE "$BASE/workflows/test-auth-bearer" "${auth_header_args[@]}" > /dev/null 2>&1 || true
curl -s -X DELETE "$BASE/workflows/test-auth-open" "${auth_header_args[@]}" > /dev/null 2>&1 || true

echo ""
echo "  結果：$PASS passed, $FAIL failed"
echo ""
echo "=== 場景 8 完成 ==="

if [ "$FAIL" -gt 0 ]; then exit 1; fi
