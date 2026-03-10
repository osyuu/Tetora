#!/usr/bin/env bash
# ============================================================
# 場景 10：Condition 分支 + External Step 組合
# ============================================================
#
# 使用情境：
#   Workflow 先做判斷（condition），然後不同分支各有 external step。
#   例如：內容審核結果分流、A/B 測試走不同外部服務、
#        支付失敗 fallback 到備用閘道。
#
# 測試子場景：
#   A) condition → then(external) → notify — 只走 then 分支
#   B) condition → else(external) → notify — 只走 else 分支
#   C) 支付 fallback：external timeout=skip → condition → 備用 external
#
# 覆蓋 spec 場景：
#   - R4: 內容審核分流（single + condition）
#   - R6: condition then/else 各有 external
#   - R9: 支付閘道 fallback（timeout→skip→condition else）
#   - R10: A/B 測試 condition → 不同 external
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

wait_for_status() {
  local workflow="$1"
  local expected="$2"
  local max_wait="${3:-30}"
  local elapsed=0
  while [ $elapsed -lt $max_wait ]; do
    local status=$(curl -s "$BASE/workflows/$workflow/runs" "${header_args[@]}" | python3 -c "
import json, sys
data = json.load(sys.stdin)
if data:
    print(data[0].get('status', ''))
" 2>/dev/null || true)
    if [ "$status" = "$expected" ]; then
      echo "$status"
      return 0
    fi
    if [ "$status" != "running" ] && [ "$status" != "waiting" ] && [ "$status" != "" ]; then
      echo "$status"
      return 1
    fi
    sleep 2
    elapsed=$((elapsed + 2))
  done
  echo "timeout_waiting"
  return 1
}

echo "=== 場景 10：Condition + External 組合 ==="
echo ""

# ── 子場景 A：內容審核 → approved → external publish ──
echo "━━━ 子場景 A：內容審核分流 — then 分支 ━━━"
echo ""

echo "[A-1] 建立 workflow"
curl -s -X DELETE "$BASE/workflows/test-condition-external" "${header_args[@]}" > /dev/null 2>&1 || true
curl -s -X POST "$BASE/workflows" \
  "${header_args[@]}" \
  -d '{
    "name": "test-condition-external",
    "description": "Test: condition → external on each branch",
    "variables": {"content_id": ""},
    "steps": [
      {
        "id": "review",
        "type": "external",
        "externalUrl": "https://httpbin.org/post",
        "externalBody": {"content_id": "{{content_id}}"},
        "callbackKey": "review-{{runId}}",
        "callbackTimeout": "2m",
        "callbackAuth": "open",
        "onTimeout": "stop"
      },
      {
        "id": "check-verdict",
        "type": "condition",
        "if": "{{steps.review.output.verdict}} == approved",
        "then": "publish",
        "else": "reject-notify",
        "dependsOn": ["review"]
      },
      {
        "id": "publish",
        "type": "external",
        "externalUrl": "https://httpbin.org/post",
        "externalBody": {
          "content_id": "{{content_id}}",
          "action": "publish",
          "reviewer_note": "{{steps.review.output.note}}"
        },
        "callbackKey": "publish-{{runId}}",
        "callbackTimeout": "2m",
        "callbackAuth": "open",
        "onTimeout": "stop",
        "dependsOn": ["check-verdict"]
      },
      {
        "id": "reject-notify",
        "type": "notify",
        "notifyMsg": "Content {{content_id}} rejected: {{steps.review.output.reason}}",
        "notifyTo": "log",
        "dependsOn": ["check-verdict"]
      },
      {
        "id": "done",
        "type": "notify",
        "notifyMsg": "Content {{content_id}} published! Publish result: {{steps.publish.output}}",
        "notifyTo": "log",
        "dependsOn": ["publish"]
      }
    ]
  }' > /dev/null

echo "[A-2] 觸發 run"
curl -s -X POST "$BASE/workflows/test-condition-external/run" \
  "${header_args[@]}" \
  -d '{"variables": {"content_id": "POST-001"}}' > /dev/null

CB_KEY=$(find_callback_key "review-" 10)
if [ -z "$CB_KEY" ]; then echo "  ✗ 找不到 review key"; exit 1; fi
echo "  ✓ review callback: $CB_KEY"

echo "[A-3] 模擬審核結果 → approved"
curl -s -X POST "$BASE/api/callbacks/$CB_KEY" \
  -H "Content-Type: application/json" \
  -d '{"verdict": "approved", "note": "Good quality content"}'
echo ""

sleep 2

echo "[A-4] 等待 publish callback 註冊（condition → then → external）"
CB_KEY2=$(find_callback_key "publish-" 10)
if [ -z "$CB_KEY2" ]; then
  echo "  ✗ publish callback 未出現 — condition 分支可能未走 then"
  exit 1
fi
echo "  ✓ publish callback: $CB_KEY2"
echo "  ✓ Condition 正確走了 then 分支"

echo "[A-5] 模擬發布完成"
curl -s -X POST "$BASE/api/callbacks/$CB_KEY2" \
  -H "Content-Type: application/json" \
  -d '{"published_url": "https://example.com/post-001", "status": "live"}'
echo ""

sleep 3
STATUS=$(curl -s "$BASE/workflows/test-condition-external/runs" "${header_args[@]}" | python3 -c "
import json, sys
data = json.load(sys.stdin)
if data: print(data[0].get('status', ''))
" 2>/dev/null || true)
if [ "$STATUS" = "completed" ]; then
  echo "  ✓ Then 分支（審核通過 → 發布）完成"
else
  echo "  ⚠ 預期 completed，實際: $STATUS"
fi
echo ""

# ── 子場景 B：審核拒絕 → else 分支 ──
echo "━━━ 子場景 B：審核拒絕 — else 分支 ━━━"
echo ""

echo "[B-1] 觸發 run"
curl -s -X POST "$BASE/workflows/test-condition-external/run" \
  "${header_args[@]}" \
  -d '{"variables": {"content_id": "POST-002"}}' > /dev/null

CB_KEY3=$(find_callback_key "review-" 10)
if [ -z "$CB_KEY3" ]; then echo "  ✗ 找不到 review key"; exit 1; fi
echo "  ✓ review callback: $CB_KEY3"

echo "[B-2] 模擬審核結果 → rejected"
curl -s -X POST "$BASE/api/callbacks/$CB_KEY3" \
  -H "Content-Type: application/json" \
  -d '{"verdict": "rejected", "reason": "Violates community guidelines"}'
echo ""

sleep 3
STATUS=$(curl -s "$BASE/workflows/test-condition-external/runs" "${header_args[@]}" | python3 -c "
import json, sys
data = json.load(sys.stdin)
if data: print(data[0].get('status', ''))
" 2>/dev/null || true)
if [ "$STATUS" = "completed" ]; then
  echo "  ✓ Else 分支（拒絕 → notify）完成"
  echo "  ✓ publish step 應被 skip（不走 then）"
else
  echo "  ⚠ 預期 completed，實際: $STATUS"
fi
echo ""

curl -s -X DELETE "$BASE/workflows/test-condition-external" "${header_args[@]}" > /dev/null 2>&1 || true

# ── 子場景 C：支付 fallback（timeout→skip→condition→備用）──
echo "━━━ 子場景 C：支付 fallback — timeout skip → 備用閘道 ━━━"
echo ""

echo "[C-1] 建立 workflow"
curl -s -X DELETE "$BASE/workflows/test-payment-fallback" "${header_args[@]}" > /dev/null 2>&1 || true
curl -s -X POST "$BASE/workflows" \
  "${header_args[@]}" \
  -d '{
    "name": "test-payment-fallback",
    "description": "Test: primary payment timeout → skip → fallback gateway",
    "steps": [
      {
        "id": "primary-pay",
        "type": "external",
        "externalUrl": "https://httpbin.org/post",
        "externalBody": {"gateway": "primary"},
        "callbackKey": "pay1-{{runId}}",
        "callbackTimeout": "15s",
        "callbackAuth": "open",
        "onTimeout": "skip"
      },
      {
        "id": "check-primary",
        "type": "condition",
        "if": "{{steps.primary-pay.output.status}} == success",
        "then": "done-primary",
        "else": "fallback-pay",
        "dependsOn": ["primary-pay"]
      },
      {
        "id": "fallback-pay",
        "type": "external",
        "externalUrl": "https://httpbin.org/post",
        "externalBody": {"gateway": "fallback"},
        "callbackKey": "pay2-{{runId}}",
        "callbackTimeout": "2m",
        "callbackAuth": "open",
        "onTimeout": "stop",
        "dependsOn": ["check-primary"]
      },
      {
        "id": "done-primary",
        "type": "notify",
        "notifyMsg": "Primary payment succeeded",
        "notifyTo": "log",
        "dependsOn": ["check-primary"]
      },
      {
        "id": "done-fallback",
        "type": "notify",
        "notifyMsg": "Fallback payment result: {{steps.fallback-pay.output}}",
        "notifyTo": "log",
        "dependsOn": ["fallback-pay"]
      }
    ]
  }' > /dev/null

echo "[C-2] 觸發 run（不送 primary callback → 等 15 秒 timeout）"
curl -s -X POST "$BASE/workflows/test-payment-fallback/run" "${header_args[@]}" -d '{}' > /dev/null
echo "  等待 primary timeout（15 秒）..."

echo "[C-3] 等待 fallback callback 註冊..."
CB_KEY_FB=$(find_callback_key "pay2-" 30)
if [ -z "$CB_KEY_FB" ]; then
  echo "  ✗ fallback callback 未出現 — timeout→skip→condition 流程可能有問題"
  curl -s "$BASE/workflows/test-payment-fallback/runs" "${header_args[@]}" | python3 -m json.tool 2>/dev/null
  exit 1
fi
echo "  ✓ fallback callback: $CB_KEY_FB"
echo "  ✓ Primary timeout → skip → condition(else) → fallback 正確觸發"

echo "[C-4] 模擬 fallback 付款成功"
curl -s -X POST "$BASE/api/callbacks/$CB_KEY_FB" \
  -H "Content-Type: application/json" \
  -d '{"status": "success", "gateway": "fallback", "ref": "FB-123"}'
echo ""

sleep 3
STATUS=$(curl -s "$BASE/workflows/test-payment-fallback/runs" "${header_args[@]}" | python3 -c "
import json, sys
data = json.load(sys.stdin)
if data: print(data[0].get('status', ''))
" 2>/dev/null || true)
if [ "$STATUS" = "completed" ]; then
  echo "  ✓ Fallback 付款流程完成"
else
  echo "  ⚠ 預期 completed，實際: $STATUS"
fi
echo ""

curl -s -X DELETE "$BASE/workflows/test-payment-fallback" "${header_args[@]}" > /dev/null 2>&1 || true

echo "=== 場景 10 完成 ==="
