#!/usr/bin/env bash
# ============================================================
# 場景 4：Timeout 行為驗證 — stop vs skip
# ============================================================
#
# 使用情境：
#   外部服務沒有回應時，workflow 的行為取決於 onTimeout 設定：
#   - "stop"：整個 workflow 標記失敗
#   - "skip"：跳過該步驟，繼續下一步（output 為空）
#
# 操作流程：
#   A) 建立 timeout=15s + onTimeout=stop 的 workflow → 等 timeout → 預期失敗
#   B) 建立 timeout=15s + onTimeout=skip 的 workflow → 等 timeout → 預期繼續
#
# 使用方式：
#   ./examples/test-external-timeout.sh [TETORA_URL]
#   注意：此測試需要等待 timeout，總共約 40 秒
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
    if [ "$status" != "running" ] && [ "$status" != "" ]; then
      echo "$status"
      return 1
    fi
    sleep 2
    elapsed=$((elapsed + 2))
  done
  echo "timeout_waiting"
  return 1
}

echo "=== 場景 4：Timeout 行為驗證 ==="
echo ""

# ── 子場景 A：onTimeout=stop ──
echo "━━━ 子場景 A：onTimeout=stop（預期 workflow 失敗）━━━"
echo ""

echo "[A-1] 建立 workflow: test-timeout-stop"
curl -s -X DELETE "$BASE/workflows/test-timeout-stop" "${header_args[@]}" > /dev/null 2>&1 || true
curl -s -X POST "$BASE/workflows" \
  "${header_args[@]}" \
  -d '{
    "name": "test-timeout-stop",
    "description": "Test: external step timeout with stop behavior",
    "steps": [
      {
        "id": "wait-forever",
        "type": "external",
        "externalUrl": "https://httpbin.org/post",
        "externalBody": {"test": "timeout"},
        "callbackKey": "timeout-stop-{{runId}}",
        "callbackTimeout": "15s",
        "callbackAuth": "open",
        "onTimeout": "stop"
      },
      {
        "id": "should-not-run",
        "type": "notify",
        "notifyMsg": "This should NOT appear if timeout=stop works",
        "notifyTo": "log",
        "dependsOn": ["wait-forever"]
      }
    ]
  }' | python3 -m json.tool 2>/dev/null
echo ""

echo "[A-2] 觸發 run"
curl -s -X POST "$BASE/workflows/test-timeout-stop/run" \
  "${header_args[@]}" \
  -d '{}' | python3 -m json.tool 2>/dev/null
echo ""

echo "[A-3] 等待 timeout（最多 30 秒）..."
STATUS=$(wait_for_status "test-timeout-stop" "failed" 30)
if [ "$STATUS" = "failed" ]; then
  echo "  ✓ Workflow 正確失敗（onTimeout=stop）"
else
  echo "  ⚠ 預期 failed，實際: $STATUS"
fi
echo ""

# ── 子場景 B：onTimeout=skip ──
echo "━━━ 子場景 B：onTimeout=skip（預期 workflow 繼續）━━━"
echo ""

echo "[B-1] 建立 workflow: test-timeout-skip"
curl -s -X DELETE "$BASE/workflows/test-timeout-skip" "${header_args[@]}" > /dev/null 2>&1 || true
curl -s -X POST "$BASE/workflows" \
  "${header_args[@]}" \
  -d '{
    "name": "test-timeout-skip",
    "description": "Test: external step timeout with skip behavior",
    "steps": [
      {
        "id": "optional-check",
        "type": "external",
        "externalUrl": "https://httpbin.org/post",
        "externalBody": {"test": "timeout-skip"},
        "callbackKey": "timeout-skip-{{runId}}",
        "callbackTimeout": "15s",
        "callbackAuth": "open",
        "onTimeout": "skip"
      },
      {
        "id": "continue-anyway",
        "type": "notify",
        "notifyMsg": "Workflow continued after skip! Optional output was: {{steps.optional-check.output}}",
        "notifyTo": "log",
        "dependsOn": ["optional-check"]
      }
    ]
  }' | python3 -m json.tool 2>/dev/null
echo ""

echo "[B-2] 觸發 run"
curl -s -X POST "$BASE/workflows/test-timeout-skip/run" \
  "${header_args[@]}" \
  -d '{}' | python3 -m json.tool 2>/dev/null
echo ""

echo "[B-3] 等待 timeout + workflow 完成（最多 30 秒）..."
STATUS=$(wait_for_status "test-timeout-skip" "completed" 30)
if [ "$STATUS" = "completed" ]; then
  echo "  ✓ Workflow 正確跳過並完成（onTimeout=skip）"
else
  echo "  ⚠ 預期 completed，實際: $STATUS"
fi
echo ""

# ── Cleanup ──
echo "[cleanup] 刪除測試 workflows"
curl -s -X DELETE "$BASE/workflows/test-timeout-stop" "${header_args[@]}" > /dev/null 2>&1 || true
curl -s -X DELETE "$BASE/workflows/test-timeout-skip" "${header_args[@]}" > /dev/null 2>&1 || true
echo "  done"
echo ""
echo "=== 場景 4 完成 ==="
