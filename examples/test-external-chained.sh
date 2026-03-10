#!/usr/bin/env bash
# ============================================================
# 場景 3：訂單流程 — 串聯多個 External Step 驗證腳本
# ============================================================
#
# 使用情境：
#   電商訂單處理 — 先付款（external），付完再出貨（external），
#   第二步用第一步 callback 回來的 reference_id。
#   最後通知客戶（用 notify 代替 dispatch 以簡化測試）。
#
# 操作流程：
#   1. 建立 workflow（payment → shipping → notify，串聯 dependsOn）
#   2. 觸發 run，帶入 order_id
#   3. 等第一個 callback（payment）→ 模擬付款成功回傳 reference_id
#   4. Workflow 自動進入第二步 → 等第二個 callback（shipping）
#   5. 模擬出貨服務回傳 tracking_number
#   6. Workflow 完成 → 通知步驟用前兩步的 output
#
# 驗證重點：
#   - 串聯的 external step 能正確傳遞 output
#   - 第二個 step 的 callbackKey 不會與第一個衝突
#   - 兩個 callback 各自獨立等待
#
# 使用方式：
#   ./examples/test-external-chained.sh [TETORA_URL]
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

echo "=== 場景 3：訂單流程（串聯 External Steps）==="
echo ""

# ── Step 1: 建立 workflow ──
echo "[1/7] 建立 workflow: test-order-fulfillment"
curl -s -X DELETE "$BASE/workflows/test-order-fulfillment" "${header_args[@]}" > /dev/null 2>&1 || true
RESULT=$(curl -s -w "\n%{http_code}" -X POST "$BASE/workflows" \
  "${header_args[@]}" \
  -d '{
    "name": "test-order-fulfillment",
    "description": "Test: chained external steps — payment then shipping",
    "variables": {
      "order_id": "",
      "customer_email": ""
    },
    "steps": [
      {
        "id": "process-payment",
        "type": "external",
        "externalUrl": "https://httpbin.org/post",
        "externalHeaders": {
          "X-Idempotency-Key": "{{order_id}}-{{runId}}"
        },
        "externalBody": {
          "order_id": "{{order_id}}",
          "callback_url": "'"$BASE"'/api/callbacks/payment-{{runId}}"
        },
        "callbackKey": "payment-{{runId}}",
        "callbackTimeout": "2m",
        "callbackAuth": "open",
        "onTimeout": "stop"
      },
      {
        "id": "trigger-shipping",
        "type": "external",
        "externalUrl": "https://httpbin.org/post",
        "externalBody": {
          "order_id": "{{order_id}}",
          "payment_ref": "{{steps.process-payment.output.reference_id}}",
          "callback_url": "'"$BASE"'/api/callbacks/shipping-{{runId}}"
        },
        "callbackKey": "shipping-{{runId}}",
        "callbackTimeout": "2m",
        "callbackAuth": "open",
        "onTimeout": "skip",
        "dependsOn": ["process-payment"]
      },
      {
        "id": "notify-customer",
        "type": "notify",
        "notifyMsg": "Order {{order_id}} complete! Payment: {{steps.process-payment.output.reference_id}}, Tracking: {{steps.trigger-shipping.output.tracking_number}}",
        "notifyTo": "log",
        "dependsOn": ["trigger-shipping"]
      }
    ]
  }')
HTTP_CODE=$(echo "$RESULT" | tail -1)
BODY=$(echo "$RESULT" | sed '$d')
echo "  Response ($HTTP_CODE): $BODY"
echo ""

# ── Step 2: 觸發 run ──
echo "[2/7] 觸發 workflow run"
curl -s -X POST "$BASE/workflows/test-order-fulfillment/run" \
  "${header_args[@]}" \
  -d '{"variables": {"order_id": "ORD-789", "customer_email": "test@example.com"}}' | python3 -m json.tool 2>/dev/null
echo ""

# ── Step 3: 等待第一個 callback（payment）──
echo "[3/7] 等待 payment callback 註冊..."
CB_KEY_PAY=$(find_callback_key "payment-" 15)
if [ -z "$CB_KEY_PAY" ]; then
  echo "  ✗ 找不到 payment callback key，中止"
  exit 1
fi
echo "  ✓ payment callback key: $CB_KEY_PAY"
echo ""

# ── Step 4: 模擬付款成功 ──
echo "[4/7] 模擬付款服務回傳結果"
RESULT=$(curl -s -w "\n%{http_code}" -X POST "$BASE/api/callbacks/$CB_KEY_PAY" \
  -H "Content-Type: application/json" \
  -d '{
    "status": "ok",
    "reference_id": "PAY-456",
    "amount": 15000,
    "currency": "TWD"
  }')
HTTP_CODE=$(echo "$RESULT" | tail -1)
BODY=$(echo "$RESULT" | sed '$d')
echo "  Response ($HTTP_CODE): $BODY"
echo ""

# ── Step 5: 等待第二個 callback（shipping）──
echo "[5/7] 等待 shipping callback 註冊（第二步自動開始）..."
CB_KEY_SHIP=$(find_callback_key "shipping-" 15)
if [ -z "$CB_KEY_SHIP" ]; then
  echo "  ✗ 找不到 shipping callback key"
  echo "  可能第一步 output 傳遞有問題，查詢 runs:"
  curl -s "$BASE/workflows/test-order-fulfillment/runs" "${header_args[@]}" | python3 -m json.tool 2>/dev/null
  exit 1
fi
echo "  ✓ shipping callback key: $CB_KEY_SHIP"
echo "  （確認：兩個 key 不同 → payment≠shipping）"
echo "    payment:  $CB_KEY_PAY"
echo "    shipping: $CB_KEY_SHIP"
echo ""

# ── Step 6: 模擬出貨完成 ──
echo "[6/7] 模擬出貨服務回傳結果"
RESULT=$(curl -s -w "\n%{http_code}" -X POST "$BASE/api/callbacks/$CB_KEY_SHIP" \
  -H "Content-Type: application/json" \
  -d '{
    "status": "shipped",
    "tracking_number": "SF1234567890",
    "carrier": "SF Express",
    "estimated_delivery": "2026-03-15"
  }')
HTTP_CODE=$(echo "$RESULT" | tail -1)
BODY=$(echo "$RESULT" | sed '$d')
echo "  Response ($HTTP_CODE): $BODY"
echo ""

# ── Step 7: 確認 workflow 完成 ──
echo "[7/7] 等待 workflow 完成（3 秒）..."
sleep 3
echo "  查詢 workflow runs:"
RUNS=$(curl -s "$BASE/workflows/test-order-fulfillment/runs" "${header_args[@]}")
echo "$RUNS" | python3 -c "
import json, sys
data = json.load(sys.stdin)
if data:
    run = data[0]
    status = run.get('status', '?')
    print(f\"  run_id={run.get('run_id','?')[:8]}... status={status}\")
    if status == 'completed':
        print('  ✓ 串聯 external steps 全部完成!')
    else:
        print(f'  ⚠ 預期 completed，實際 {status}')
" 2>/dev/null || echo "  $RUNS"
echo ""

# ── 驗證：重複 callback 應該被拒絕 ──
echo "━━━ 附加驗證：重複 callback ━━━"
echo ""
echo "  嘗試再次送 payment callback（應該回 already_delivered）:"
RESULT=$(curl -s -w "\n%{http_code}" -X POST "$BASE/api/callbacks/$CB_KEY_PAY" \
  -H "Content-Type: application/json" \
  -d '{"status": "duplicate_attempt"}')
HTTP_CODE=$(echo "$RESULT" | tail -1)
BODY=$(echo "$RESULT" | sed '$d')
echo "  Response ($HTTP_CODE): $BODY"
EXPECTED="already_delivered"
if echo "$BODY" | grep -q "$EXPECTED"; then
  echo "  ✓ 正確拒絕重複 callback"
else
  echo "  ⚠ 預期包含 '$EXPECTED'"
fi
echo ""

# ── Cleanup ──
echo "[cleanup] 刪除測試 workflow"
curl -s -X DELETE "$BASE/workflows/test-order-fulfillment" "${header_args[@]}" | python3 -m json.tool 2>/dev/null || true
echo ""
echo "=== 場景 3 完成 ==="
