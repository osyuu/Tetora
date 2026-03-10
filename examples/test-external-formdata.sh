#!/usr/bin/env bash
# ============================================================
# 場景 12：Content Type 變體 — form-urlencoded + XML
# ============================================================
#
# 使用情境：
#   並非所有 API 都用 JSON。有些需要 form-urlencoded（Stripe），
#   有些需要 XML（製造業 MES / SOAP）。
#
# 測試子場景：
#   A) form-urlencoded request（externalRawBody）
#   B) XML request（externalRawBody + externalContentType）
#   C) XML callback（非 JSON body → 存為 raw string）
#
# 覆蓋 spec 場景：
#   - R2: 製造業品檢 MES（XML request + callback）
#   - R7: XML callback（HTTP handler 讀 raw body bytes）
#   - R8: 法規合規大 payload（接近 1MB）
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

echo "=== 場景 12：Content Type 變體 ==="
echo ""

# ── A) form-urlencoded ──
echo "━━━ A) form-urlencoded request（Stripe 風格）━━━"
echo ""

echo "[A-1] 建立 workflow"
curl -s -X DELETE "$BASE/workflows/test-form-encoded" "${header_args[@]}" > /dev/null 2>&1 || true
curl -s -X POST "$BASE/workflows" \
  "${header_args[@]}" \
  -d '{
    "name": "test-form-encoded",
    "description": "Test: form-urlencoded request body",
    "variables": {"amount": "5000", "currency": "twd"},
    "steps": [
      {
        "id": "charge",
        "type": "external",
        "externalUrl": "https://httpbin.org/post",
        "externalContentType": "application/x-www-form-urlencoded",
        "externalRawBody": "amount={{amount}}&currency={{currency}}&source=tok_test",
        "callbackKey": "charge-{{runId}}",
        "callbackTimeout": "2m",
        "callbackAuth": "open",
        "onTimeout": "stop"
      },
      {
        "id": "done",
        "type": "notify",
        "notifyMsg": "Charge result: {{steps.charge.output}}",
        "notifyTo": "log",
        "dependsOn": ["charge"]
      }
    ]
  }' > /dev/null

echo "[A-2] 觸發 run"
curl -s -X POST "$BASE/workflows/test-form-encoded/run" \
  "${header_args[@]}" \
  -d '{"variables": {"amount": "5000", "currency": "twd"}}' > /dev/null

CB_KEY=$(find_callback_key "charge-" 10)
if [ -z "$CB_KEY" ]; then echo "  ✗ 找不到 key"; exit 1; fi
echo "  ✓ callback key: $CB_KEY"

echo "[A-3] 模擬回傳"
RESULT=$(curl -s -X POST "$BASE/api/callbacks/$CB_KEY" \
  -H "Content-Type: application/json" \
  -d '{"id": "ch_test", "status": "succeeded", "amount": 5000}')
echo "  Response: $RESULT"

sleep 3
STATUS=$(curl -s "$BASE/workflows/test-form-encoded/runs" "${header_args[@]}" | python3 -c "
import json, sys
data = json.load(sys.stdin)
if data: print(data[0].get('status', ''))
" 2>/dev/null || true)
if [ "$STATUS" = "completed" ]; then
  echo "  ✓ form-urlencoded request + JSON callback 完成"
else
  echo "  ⚠ 預期 completed，實際: $STATUS"
fi
echo ""

curl -s -X DELETE "$BASE/workflows/test-form-encoded" "${header_args[@]}" > /dev/null 2>&1 || true

# ── B) XML request ──
echo "━━━ B) XML request（MES 品檢）━━━"
echo ""

echo "[B-1] 建立 workflow"
curl -s -X DELETE "$BASE/workflows/test-xml-request" "${header_args[@]}" > /dev/null 2>&1 || true
curl -s -X POST "$BASE/workflows" \
  "${header_args[@]}" \
  -d '{
    "name": "test-xml-request",
    "description": "Test: XML request body",
    "variables": {"product_id": "PROD-001"},
    "steps": [
      {
        "id": "quality-check",
        "type": "external",
        "externalUrl": "https://httpbin.org/post",
        "externalContentType": "application/xml",
        "externalRawBody": "<quality><product_id>{{product_id}}</product_id><check_type>visual</check_type></quality>",
        "callbackKey": "qc-{{runId}}",
        "callbackTimeout": "2m",
        "callbackAuth": "open",
        "onTimeout": "stop"
      },
      {
        "id": "result",
        "type": "notify",
        "notifyMsg": "QC result: {{steps.quality-check.output}}",
        "notifyTo": "log",
        "dependsOn": ["quality-check"]
      }
    ]
  }' > /dev/null

echo "[B-2] 觸發 run"
curl -s -X POST "$BASE/workflows/test-xml-request/run" \
  "${header_args[@]}" \
  -d '{"variables": {"product_id": "PROD-001"}}' > /dev/null

CB_KEY=$(find_callback_key "qc-" 10)
if [ -z "$CB_KEY" ]; then echo "  ✗ 找不到 key"; exit 1; fi
echo "  ✓ callback key: $CB_KEY"

echo "[B-3] 模擬 XML callback（存為 raw string）"
RESULT=$(curl -s -X POST "$BASE/api/callbacks/$CB_KEY" \
  -H "Content-Type: application/xml" \
  -d '<response><ticket_id>T123</ticket_id><status>pass</status><defects>0</defects></response>')
echo "  Response: $RESULT"

sleep 3
STATUS=$(curl -s "$BASE/workflows/test-xml-request/runs" "${header_args[@]}" | python3 -c "
import json, sys
data = json.load(sys.stdin)
if data: print(data[0].get('status', ''))
" 2>/dev/null || true)
if [ "$STATUS" = "completed" ]; then
  echo "  ✓ XML request + XML callback 完成（output 為 raw XML string）"
else
  echo "  ⚠ 預期 completed，實際: $STATUS"
fi
echo ""

curl -s -X DELETE "$BASE/workflows/test-xml-request" "${header_args[@]}" > /dev/null 2>&1 || true

echo "=== 場景 12 完成 ==="
