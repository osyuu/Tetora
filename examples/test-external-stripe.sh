#!/usr/bin/env bash
# ============================================================
# 場景 2：Stripe 退款 + 條件分支 — External Step 驗證腳本
# ============================================================
#
# 使用情境：
#   客服要處理退款。Stripe 退款是非同步的，完成後透過 webhook 通知。
#   Webhook body 是巢狀的（data.object.status），需要 ResponseMapping 提取。
#   退款成功/失敗走不同通知路線。
#
# 操作流程：
#   1. 建立 workflow（external + condition + 2x notify）
#   2. 觸發 run，帶入 charge_id, refund_amount
#   3. Tetora POST 到 Stripe（本測試用 httpbin 代替）
#   4. 模擬 Stripe webhook callback（巢狀 JSON，用 ResponseMapping 提取）
#   5. Condition step 判斷 status → 走成功 or 失敗通知
#
# 本腳本測試兩個子場景：
#   A) 退款成功（status=succeeded）
#   B) 退款失敗（status=failed）
#
# 使用方式：
#   ./examples/test-external-stripe.sh [TETORA_URL]
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
  local max_wait="${2:-10}"
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

echo "=== 場景 2：Stripe 退款 + 條件分支 ==="
echo ""

# ── Step 1: 建立 workflow ──
echo "[1/6] 建立 workflow: test-stripe-refund"
curl -s -X DELETE "$BASE/workflows/test-stripe-refund" "${header_args[@]}" > /dev/null 2>&1 || true
RESULT=$(curl -s -w "\n%{http_code}" -X POST "$BASE/workflows" \
  "${header_args[@]}" \
  -d '{
    "name": "test-stripe-refund",
    "description": "Test: Stripe refund with ResponseMapping and condition branching",
    "variables": {
      "charge_id": "",
      "refund_amount": ""
    },
    "steps": [
      {
        "id": "create-refund",
        "type": "external",
        "externalUrl": "https://httpbin.org/post",
        "externalContentType": "application/x-www-form-urlencoded",
        "externalRawBody": "charge={{charge_id}}&amount={{refund_amount}}",
        "callbackKey": "stripe-refund-{{runId}}",
        "callbackTimeout": "2m",
        "callbackAuth": "open",
        "callbackResponseMap": {
          "statusPath": "data.object.status",
          "dataPath": "data.object"
        },
        "onTimeout": "stop"
      },
      {
        "id": "check-status",
        "type": "condition",
        "if": "{{steps.create-refund.output.status}} == succeeded",
        "then": "notify-success",
        "else": "notify-failure",
        "dependsOn": ["create-refund"]
      },
      {
        "id": "notify-success",
        "type": "notify",
        "notifyMsg": "Refund succeeded for charge {{charge_id}}: amount={{steps.create-refund.output.amount}}",
        "notifyTo": "log",
        "dependsOn": ["check-status"]
      },
      {
        "id": "notify-failure",
        "type": "notify",
        "notifyMsg": "Refund FAILED for charge {{charge_id}}: {{steps.create-refund.output.failure_reason}}",
        "notifyTo": "log",
        "dependsOn": ["check-status"]
      }
    ]
  }')
HTTP_CODE=$(echo "$RESULT" | tail -1)
BODY=$(echo "$RESULT" | sed '$d')
echo "  Response ($HTTP_CODE): $BODY"
echo ""

# ── 子場景 A：退款成功 ──
echo "━━━ 子場景 A：退款成功 ━━━"
echo ""

echo "[2/6] 觸發 run（charge_id=ch_success_test）"
curl -s -X POST "$BASE/workflows/test-stripe-refund/run" \
  "${header_args[@]}" \
  -d '{"variables": {"charge_id": "ch_success_test", "refund_amount": "5000"}}' | python3 -m json.tool 2>/dev/null
echo ""

echo "[3/6] 等待 callback 註冊..."
CB_KEY=$(find_callback_key "stripe-refund-" 10)
if [ -z "$CB_KEY" ]; then
  echo "  ✗ 找不到 callback key，中止"
  exit 1
fi
echo "  ✓ callback key: $CB_KEY"
echo ""

echo "[4/6] 模擬 Stripe webhook（退款成功）"
echo "  送出巢狀 JSON — data.object.status=succeeded"
RESULT=$(curl -s -w "\n%{http_code}" -X POST "$BASE/api/callbacks/$CB_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "type": "refund.updated",
    "data": {
      "object": {
        "id": "re_test123",
        "status": "succeeded",
        "amount": 5000,
        "currency": "twd",
        "charge": "ch_success_test",
        "failure_reason": null
      }
    }
  }')
HTTP_CODE=$(echo "$RESULT" | tail -1)
BODY=$(echo "$RESULT" | sed '$d')
echo "  Response ($HTTP_CODE): $BODY"
echo ""

echo "  等待 workflow 完成（3 秒）..."
sleep 3

echo "  查詢 runs — 預期：completed，且走了 notify-success 分支"
RUNS=$(curl -s "$BASE/workflows/test-stripe-refund/runs" "${header_args[@]}")
echo "$RUNS" | python3 -c "
import json, sys
data = json.load(sys.stdin)
if data:
    run = data[0]
    print(f\"  run_id={run.get('run_id','?')[:8]}... status={run.get('status','?')}\")
    steps = run.get('step_results', {})
    if isinstance(steps, dict):
        for sid, sr in steps.items():
            print(f\"    step={sid} status={sr.get('status','?') if isinstance(sr, dict) else sr}\")
" 2>/dev/null || echo "  $RUNS"
echo ""

# ── 子場景 B：退款失敗 ──
echo "━━━ 子場景 B：退款失敗 ━━━"
echo ""

echo "[5/6] 觸發第二次 run（charge_id=ch_fail_test）"
curl -s -X POST "$BASE/workflows/test-stripe-refund/run" \
  "${header_args[@]}" \
  -d '{"variables": {"charge_id": "ch_fail_test", "refund_amount": "9999"}}' | python3 -m json.tool 2>/dev/null
echo ""

echo "  等待 callback 註冊..."
CB_KEY2=$(find_callback_key "stripe-refund-" 10)
if [ -z "$CB_KEY2" ]; then
  echo "  ✗ 找不到 callback key，中止"
  exit 1
fi
echo "  ✓ callback key: $CB_KEY2"
echo ""

echo "[6/6] 模擬 Stripe webhook（退款失敗）"
RESULT=$(curl -s -w "\n%{http_code}" -X POST "$BASE/api/callbacks/$CB_KEY2" \
  -H "Content-Type: application/json" \
  -d '{
    "type": "refund.updated",
    "data": {
      "object": {
        "id": "re_test456",
        "status": "failed",
        "amount": 9999,
        "currency": "twd",
        "charge": "ch_fail_test",
        "failure_reason": "insufficient_funds"
      }
    }
  }')
HTTP_CODE=$(echo "$RESULT" | tail -1)
BODY=$(echo "$RESULT" | sed '$d')
echo "  Response ($HTTP_CODE): $BODY"
echo ""

echo "  等待 workflow 完成（3 秒）..."
sleep 3

echo "  查詢 runs — 預期：completed，且走了 notify-failure 分支"
RUNS=$(curl -s "$BASE/workflows/test-stripe-refund/runs" "${header_args[@]}")
echo "$RUNS" | python3 -c "
import json, sys
data = json.load(sys.stdin)
if data:
    run = data[0]
    print(f\"  run_id={run.get('run_id','?')[:8]}... status={run.get('status','?')}\")
" 2>/dev/null || echo "  $RUNS"
echo ""

# ── Cleanup ──
echo "[cleanup] 刪除測試 workflow"
curl -s -X DELETE "$BASE/workflows/test-stripe-refund" "${header_args[@]}" | python3 -m json.tool 2>/dev/null || true
echo ""
echo "=== 場景 2 完成 ==="
