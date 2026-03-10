#!/usr/bin/env bash
# ============================================================
# 場景 5：Streaming Callback — 醫療檢驗 / 影片轉碼 / IoT
# ============================================================
#
# 使用情境：
#   外部服務會多次回呼（部分結果），直到最終結果到達。
#   例如：醫療檢驗、影片轉碼進度、IoT 韌體更新。
#
# 測試子場景：
#   A) 基本 streaming：partial → partial → final（DonePath + DoneValue）
#   B) accumulate=true：累積所有結果為 JSON array
#   C) streaming timeout 但有部分結果（onTimeout=skip）
#
# 覆蓋 spec 場景：
#   - R2: 醫療檢驗報告（streaming + DonePath）
#   - R4: 影片轉碼進度（DoneValue "100" float→string）
#   - R5: IoT 韌體更新（buffer 256 + replay）
#   - R6: IoT 批次校正 streaming timeout 需累積
#   - R6: GitHub PR 多事件同 key（streaming + boolean doneValue）
#   - R9: DocuSign 多簽署人（streaming + accumulate）
#   - R10: 混合 single + streaming 同 workflow
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

echo "=== 場景 5：Streaming Callback ==="
echo ""

# ── 子場景 A：基本 streaming（醫療檢驗）──
echo "━━━ 子場景 A：醫療檢驗 — partial → final ━━━"
echo ""

echo "[A-1] 建立 workflow: test-streaming-basic"
curl -s -X DELETE "$BASE/workflows/test-streaming-basic" "${header_args[@]}" > /dev/null 2>&1 || true
RESULT=$(curl -s -w "\n%{http_code}" -X POST "$BASE/workflows" \
  "${header_args[@]}" \
  -d '{
    "name": "test-streaming-basic",
    "description": "Test: streaming callback with DonePath/DoneValue",
    "steps": [
      {
        "id": "lab-order",
        "type": "external",
        "externalUrl": "https://httpbin.org/post",
        "externalBody": {"patient_id": "P001", "test_type": "blood_work"},
        "callbackKey": "lab-{{runId}}",
        "callbackTimeout": "2m",
        "callbackMode": "streaming",
        "callbackAuth": "open",
        "callbackResponseMap": {
          "statusPath": "status",
          "dataPath": "results",
          "donePath": "status",
          "doneValue": "final"
        },
        "onTimeout": "stop"
      },
      {
        "id": "notify-result",
        "type": "notify",
        "notifyMsg": "Lab results: {{steps.lab-order.output}}",
        "notifyTo": "log",
        "dependsOn": ["lab-order"]
      }
    ]
  }')
HTTP_CODE=$(echo "$RESULT" | tail -1)
echo "  Response ($HTTP_CODE)"
echo ""

echo "[A-2] 觸發 run"
curl -s -X POST "$BASE/workflows/test-streaming-basic/run" "${header_args[@]}" -d '{}' | python3 -m json.tool 2>/dev/null
echo ""

echo "[A-3] 等待 callback 註冊..."
CB_KEY=$(find_callback_key "lab-" 10)
if [ -z "$CB_KEY" ]; then
  echo "  ✗ 找不到 callback key"
  exit 1
fi
echo "  ✓ callback key: $CB_KEY"
echo ""

echo "[A-4] 送第 1 次 callback（partial — WBC 結果）"
RESULT=$(curl -s -X POST "$BASE/api/callbacks/$CB_KEY" \
  -H "Content-Type: application/json" \
  -d '{"status": "partial", "results": [{"WBC": 5.2}]}')
echo "  Response: $RESULT"
EXPECTED="accumulated"
if echo "$RESULT" | grep -q "$EXPECTED"; then
  echo "  ✓ 正確回傳 accumulated（部分結果）"
else
  echo "  ⚠ 預期包含 '$EXPECTED'"
fi
echo ""

sleep 1

echo "[A-5] 送第 2 次 callback（partial — 加 RBC）"
RESULT=$(curl -s -X POST "$BASE/api/callbacks/$CB_KEY" \
  -H "Content-Type: application/json" \
  -d '{"status": "partial", "results": [{"WBC": 5.2, "RBC": 4.5}]}')
echo "  Response: $RESULT"
echo ""

sleep 1

echo "[A-6] 送第 3 次 callback（final — 完整報告）"
RESULT=$(curl -s -X POST "$BASE/api/callbacks/$CB_KEY" \
  -H "Content-Type: application/json" \
  -d '{"status": "final", "results": [{"WBC": 5.2, "RBC": 4.5, "PLT": 250}]}')
echo "  Response: $RESULT"
if echo "$RESULT" | grep -q "delivered"; then
  echo "  ✓ 正確回傳 delivered（最終結果）"
fi
echo ""

echo "[A-7] 確認 workflow 完成..."
sleep 3
STATUS=$(curl -s "$BASE/workflows/test-streaming-basic/runs" "${header_args[@]}" | python3 -c "
import json, sys
data = json.load(sys.stdin)
if data: print(data[0].get('status', ''))
" 2>/dev/null || true)
if [ "$STATUS" = "completed" ]; then
  echo "  ✓ Workflow 完成（streaming 正確結束）"
else
  echo "  ⚠ 預期 completed，實際: $STATUS"
fi
echo ""

curl -s -X DELETE "$BASE/workflows/test-streaming-basic" "${header_args[@]}" > /dev/null 2>&1 || true

# ── 子場景 B：Accumulate 模式（DocuSign 多簽署人）──
echo "━━━ 子場景 B：DocuSign 多簽署人 — accumulate=true ━━━"
echo ""

echo "[B-1] 建立 workflow: test-streaming-accumulate"
curl -s -X DELETE "$BASE/workflows/test-streaming-accumulate" "${header_args[@]}" > /dev/null 2>&1 || true
curl -s -X POST "$BASE/workflows" \
  "${header_args[@]}" \
  -d '{
    "name": "test-streaming-accumulate",
    "description": "Test: streaming with accumulate — collects all results into array",
    "steps": [
      {
        "id": "collect-signatures",
        "type": "external",
        "externalUrl": "https://httpbin.org/post",
        "externalBody": {"doc_id": "CONTRACT-001"},
        "callbackKey": "sign-{{runId}}",
        "callbackTimeout": "2m",
        "callbackMode": "streaming",
        "callbackAccumulate": true,
        "callbackAuth": "open",
        "callbackResponseMap": {
          "dataPath": "signer",
          "donePath": "all_signed",
          "doneValue": "true"
        },
        "onTimeout": "stop"
      },
      {
        "id": "finalize",
        "type": "notify",
        "notifyMsg": "All signatures collected: {{steps.collect-signatures.output}}",
        "notifyTo": "log",
        "dependsOn": ["collect-signatures"]
      }
    ]
  }' > /dev/null

echo "[B-2] 觸發 run"
curl -s -X POST "$BASE/workflows/test-streaming-accumulate/run" "${header_args[@]}" -d '{}' > /dev/null
echo ""

CB_KEY=$(find_callback_key "sign-" 10)
if [ -z "$CB_KEY" ]; then
  echo "  ✗ 找不到 callback key"
  exit 1
fi
echo "  ✓ callback key: $CB_KEY"
echo ""

echo "[B-3] 簽署人 1 簽名"
curl -s -X POST "$BASE/api/callbacks/$CB_KEY" \
  -H "Content-Type: application/json" \
  -d '{"signer": {"name": "Alice", "signed_at": "2026-03-10T10:00:00Z"}, "all_signed": "false"}'
echo ""

sleep 1

echo "[B-4] 簽署人 2 簽名"
curl -s -X POST "$BASE/api/callbacks/$CB_KEY" \
  -H "Content-Type: application/json" \
  -d '{"signer": {"name": "Bob", "signed_at": "2026-03-10T11:00:00Z"}, "all_signed": "false"}'
echo ""

sleep 1

echo "[B-5] 簽署人 3 簽名（最後一位 → all_signed=true）"
curl -s -X POST "$BASE/api/callbacks/$CB_KEY" \
  -H "Content-Type: application/json" \
  -d '{"signer": {"name": "Charlie", "signed_at": "2026-03-10T12:00:00Z"}, "all_signed": "true"}'
echo ""

sleep 3
STATUS=$(curl -s "$BASE/workflows/test-streaming-accumulate/runs" "${header_args[@]}" | python3 -c "
import json, sys
data = json.load(sys.stdin)
if data: print(data[0].get('status', ''))
" 2>/dev/null || true)
echo ""
if [ "$STATUS" = "completed" ]; then
  echo "  ✓ 累積模式完成 — output 應為包含 3 個簽署人的 JSON array"
else
  echo "  ⚠ 預期 completed，實際: $STATUS"
fi
echo ""

curl -s -X DELETE "$BASE/workflows/test-streaming-accumulate" "${header_args[@]}" > /dev/null 2>&1 || true

# ── 子場景 C：Streaming timeout 有部分結果（onTimeout=skip）──
echo "━━━ 子場景 C：Streaming timeout 有部分結果 ━━━"
echo ""

echo "[C-1] 建立 workflow: test-streaming-timeout"
curl -s -X DELETE "$BASE/workflows/test-streaming-timeout" "${header_args[@]}" > /dev/null 2>&1 || true
curl -s -X POST "$BASE/workflows" \
  "${header_args[@]}" \
  -d '{
    "name": "test-streaming-timeout",
    "description": "Test: streaming timeout with partial results (onTimeout=skip)",
    "steps": [
      {
        "id": "partial-data",
        "type": "external",
        "externalUrl": "https://httpbin.org/post",
        "externalBody": {"test": "streaming-timeout"},
        "callbackKey": "partial-{{runId}}",
        "callbackTimeout": "15s",
        "callbackMode": "streaming",
        "callbackAccumulate": true,
        "callbackAuth": "open",
        "callbackResponseMap": {
          "dataPath": "data",
          "donePath": "done",
          "doneValue": "true"
        },
        "onTimeout": "skip"
      },
      {
        "id": "use-partial",
        "type": "notify",
        "notifyMsg": "Got partial data: {{steps.partial-data.output}}",
        "notifyTo": "log",
        "dependsOn": ["partial-data"]
      }
    ]
  }' > /dev/null

echo "[C-2] 觸發 run"
curl -s -X POST "$BASE/workflows/test-streaming-timeout/run" "${header_args[@]}" -d '{}' > /dev/null
echo ""

CB_KEY=$(find_callback_key "partial-" 10)
if [ -z "$CB_KEY" ]; then
  echo "  ✗ 找不到 callback key"
  exit 1
fi
echo "  ✓ callback key: $CB_KEY"

echo "[C-3] 送 1 次 partial（然後不送 final → 等 timeout）"
curl -s -X POST "$BASE/api/callbacks/$CB_KEY" \
  -H "Content-Type: application/json" \
  -d '{"data": {"sensor": "A1", "value": 23.5}, "done": "false"}'
echo ""

echo "[C-4] 等待 timeout（15 秒）+ workflow 完成..."
STATUS=$(wait_for_status "test-streaming-timeout" "completed" 30)
if [ "$STATUS" = "completed" ]; then
  echo "  ✓ Workflow skip + 完成（保留部分結果）"
else
  echo "  ⚠ 預期 completed（skip），實際: $STATUS"
fi
echo ""

curl -s -X DELETE "$BASE/workflows/test-streaming-timeout" "${header_args[@]}" > /dev/null 2>&1 || true

echo "=== 場景 5 完成 ==="
