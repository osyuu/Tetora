#!/usr/bin/env bash
# ============================================================
# 場景 13：Cancel 等待中的 Workflow
# ============================================================
#
# 使用情境：
#   使用者觸發了 workflow，external step 在等 callback，
#   但使用者決定取消。POST /workflow-runs/{id}/cancel 應該：
#   - 取消所有等待中的 step
#   - Workflow 標記 cancelled
#   - CallbackManager 清理 channel
#
# 覆蓋 spec 場景：
#   - R9: AI 模型訓練 48h + cancel
#   - 邊界情況: 使用者取消等待中 workflow
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

echo "=== 場景 13：Cancel 等待中的 Workflow ==="
echo ""

echo "[1] 建立 workflow: test-cancel"
curl -s -X DELETE "$BASE/workflows/test-cancel" "${header_args[@]}" > /dev/null 2>&1 || true
curl -s -X POST "$BASE/workflows" \
  "${header_args[@]}" \
  -d '{
    "name": "test-cancel",
    "description": "Test: cancel a waiting workflow",
    "steps": [{
      "id": "long-wait",
      "type": "external",
      "externalUrl": "https://httpbin.org/post",
      "externalBody": {"test": "cancel"},
      "callbackKey": "cancel-{{runId}}",
      "callbackTimeout": "5m",
      "callbackAuth": "open",
      "onTimeout": "stop"
    },
    {
      "id": "should-not-run",
      "type": "notify",
      "notifyMsg": "This should NOT run after cancel",
      "notifyTo": "log",
      "dependsOn": ["long-wait"]
    }]
  }' > /dev/null

echo "[2] 觸發 run"
curl -s -X POST "$BASE/workflows/test-cancel/run" "${header_args[@]}" -d '{}' > /dev/null
echo ""

echo "[3] 等待 external step 進入 waiting..."
CB_KEY=$(find_callback_key "cancel-" 10)
if [ -z "$CB_KEY" ]; then echo "  ✗ 找不到 callback key"; exit 1; fi
echo "  ✓ 正在等待 callback: $CB_KEY"

# 取得 run ID
RUN_ID=$(curl -s "$BASE/workflows/test-cancel/runs" "${header_args[@]}" | python3 -c "
import json, sys
data = json.load(sys.stdin)
if data: print(data[0].get('run_id', ''))
" 2>/dev/null || true)

if [ -z "$RUN_ID" ]; then
  echo "  ✗ 找不到 run_id"
  exit 1
fi
echo "  run_id: $RUN_ID"
echo ""

echo "[4] 送出 cancel 請求"
RESULT=$(curl -s -w "\n%{http_code}" -X POST "$BASE/workflow-runs/$RUN_ID/cancel" \
  "${header_args[@]}")
HTTP_CODE=$(echo "$RESULT" | tail -1)
BODY=$(echo "$RESULT" | sed '$d')
echo "  Response ($HTTP_CODE): $BODY"
echo ""

sleep 3

echo "[5] 確認 workflow 狀態"
RUNS=$(curl -s "$BASE/workflows/test-cancel/runs" "${header_args[@]}")
STATUS=$(echo "$RUNS" | python3 -c "
import json, sys
data = json.load(sys.stdin)
if data: print(data[0].get('status', ''))
" 2>/dev/null || true)

if [ "$STATUS" = "cancelled" ]; then
  echo "  ✓ Workflow 正確取消"
elif [ "$STATUS" = "failed" ] || [ "$STATUS" = "error" ]; then
  echo "  ✓ Workflow 已停止（status=$STATUS）"
else
  echo "  ⚠ 預期 cancelled，實際: $STATUS"
fi
echo ""

echo "[6] 確認 callback 已取消（送 callback 應回 404 或 already_delivered）"
RESULT=$(curl -s -w "\n%{http_code}" -X POST "$BASE/api/callbacks/$CB_KEY" \
  -H "Content-Type: application/json" \
  -d '{"result": "late-callback"}')
HTTP_CODE=$(echo "$RESULT" | tail -1)
BODY=$(echo "$RESULT" | sed '$d')
echo "  Response ($HTTP_CODE): $BODY"
if [ "$HTTP_CODE" = "404" ] || echo "$BODY" | grep -q "already_delivered\|not found\|expired"; then
  echo "  ✓ 遲到的 callback 正確被拒絕"
else
  echo "  ⚠ 預期 404 或 already_delivered"
fi
echo ""

curl -s -X DELETE "$BASE/workflows/test-cancel" "${header_args[@]}" > /dev/null 2>&1 || true

echo "=== 場景 13 完成 ==="
