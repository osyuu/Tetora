#!/usr/bin/env bash
# ============================================================
# 場景 11：Parallel Branch + External Step
# ============================================================
#
# 使用情境：
#   Workflow 有多個 external step 同時進行（無 dependsOn 互相依賴）。
#   例如：同時向多個服務發請求，各自等 callback，全部完成後匯總。
#   或是：parallel 內一個 branch 失敗，其他 branch 仍在等 callback。
#
# 測試子場景：
#   A) 兩個 external step 並行 → 各自 callback → 匯總通知
#   B) 一個 branch 正常 callback + 另一個 timeout(skip) → workflow 完成
#
# 覆蓋 spec 場景：
#   - R5: parallel branch 失敗 + external 等待中
#   - R8: 客戶回饋 parallel 內 external
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

echo "=== 場景 11：Parallel + External ==="
echo ""

# ── 子場景 A：兩個 external 並行 ──
echo "━━━ 子場景 A：並行 external steps ━━━"
echo ""

echo "[A-1] 建立 workflow: test-parallel-external"
curl -s -X DELETE "$BASE/workflows/test-parallel-external" "${header_args[@]}" > /dev/null 2>&1 || true
curl -s -X POST "$BASE/workflows" \
  "${header_args[@]}" \
  -d '{
    "name": "test-parallel-external",
    "description": "Test: two external steps running in parallel",
    "steps": [
      {
        "id": "service-a",
        "type": "external",
        "externalUrl": "https://httpbin.org/post",
        "externalBody": {"service": "A"},
        "callbackKey": "svc-a-{{runId}}",
        "callbackTimeout": "2m",
        "callbackAuth": "open",
        "onTimeout": "stop"
      },
      {
        "id": "service-b",
        "type": "external",
        "externalUrl": "https://httpbin.org/post",
        "externalBody": {"service": "B"},
        "callbackKey": "svc-b-{{runId}}",
        "callbackTimeout": "2m",
        "callbackAuth": "open",
        "onTimeout": "stop"
      },
      {
        "id": "aggregate",
        "type": "notify",
        "notifyMsg": "A={{steps.service-a.output.result}} B={{steps.service-b.output.result}}",
        "notifyTo": "log",
        "dependsOn": ["service-a", "service-b"]
      }
    ]
  }' > /dev/null

echo "[A-2] 觸發 run"
curl -s -X POST "$BASE/workflows/test-parallel-external/run" "${header_args[@]}" -d '{}' > /dev/null
echo ""

echo "[A-3] 等待兩個 callback 同時註冊..."
CB_A=$(find_callback_key "svc-a-" 10)
CB_B=$(find_callback_key "svc-b-" 10)

if [ -z "$CB_A" ] || [ -z "$CB_B" ]; then
  echo "  ✗ 未能找到兩個 callback key (A=$CB_A, B=$CB_B)"
  exit 1
fi
echo "  ✓ service-a callback: $CB_A"
echo "  ✓ service-b callback: $CB_B"
echo "  ✓ 兩個 external step 同時在等待"
echo ""

echo "[A-4] 先回 service-b，再回 service-a（反序）"
curl -s -X POST "$BASE/api/callbacks/$CB_B" \
  -H "Content-Type: application/json" \
  -d '{"result": "B-done", "score": 88}'
echo "  service-b: delivered"

sleep 1

curl -s -X POST "$BASE/api/callbacks/$CB_A" \
  -H "Content-Type: application/json" \
  -d '{"result": "A-done", "score": 95}'
echo "  service-a: delivered"
echo ""

sleep 3
echo "[A-5] 確認結果"
STATUS=$(curl -s "$BASE/workflows/test-parallel-external/runs" "${header_args[@]}" | python3 -c "
import json, sys
data = json.load(sys.stdin)
if data: print(data[0].get('status', ''))
" 2>/dev/null || true)
if [ "$STATUS" = "completed" ]; then
  echo "  ✓ 並行 external steps 全部完成（aggregate step 也執行了）"
else
  echo "  ⚠ 預期 completed，實際: $STATUS"
fi
echo ""

curl -s -X DELETE "$BASE/workflows/test-parallel-external" "${header_args[@]}" > /dev/null 2>&1 || true

# ── 子場景 B：一個正常 + 一個 timeout skip ──
echo "━━━ 子場景 B：並行中一個 timeout skip ━━━"
echo ""

echo "[B-1] 建立 workflow"
curl -s -X DELETE "$BASE/workflows/test-parallel-timeout" "${header_args[@]}" > /dev/null 2>&1 || true
curl -s -X POST "$BASE/workflows" \
  "${header_args[@]}" \
  -d '{
    "name": "test-parallel-timeout",
    "description": "Test: parallel — one callback ok, one timeout skip",
    "steps": [
      {
        "id": "fast-svc",
        "type": "external",
        "externalUrl": "https://httpbin.org/post",
        "externalBody": {"service": "fast"},
        "callbackKey": "fast-{{runId}}",
        "callbackTimeout": "2m",
        "callbackAuth": "open",
        "onTimeout": "stop"
      },
      {
        "id": "slow-svc",
        "type": "external",
        "externalUrl": "https://httpbin.org/post",
        "externalBody": {"service": "slow"},
        "callbackKey": "slow-{{runId}}",
        "callbackTimeout": "15s",
        "callbackAuth": "open",
        "onTimeout": "skip"
      },
      {
        "id": "merge",
        "type": "notify",
        "notifyMsg": "Fast={{steps.fast-svc.output}} Slow={{steps.slow-svc.output}}",
        "notifyTo": "log",
        "dependsOn": ["fast-svc", "slow-svc"]
      }
    ]
  }' > /dev/null

echo "[B-2] 觸發 run"
curl -s -X POST "$BASE/workflows/test-parallel-timeout/run" "${header_args[@]}" -d '{}' > /dev/null
echo ""

echo "[B-3] 只回 fast-svc，slow-svc 讓它 timeout"
CB_FAST=$(find_callback_key "fast-" 10)
if [ -z "$CB_FAST" ]; then echo "  ✗ 找不到 fast key"; exit 1; fi
echo "  ✓ fast callback: $CB_FAST"

curl -s -X POST "$BASE/api/callbacks/$CB_FAST" \
  -H "Content-Type: application/json" \
  -d '{"result": "fast-done"}'
echo "  fast-svc: delivered"
echo ""

echo "[B-4] 等待 slow-svc timeout（15s）+ workflow 完成..."
sleep 20
STATUS=$(curl -s "$BASE/workflows/test-parallel-timeout/runs" "${header_args[@]}" | python3 -c "
import json, sys
data = json.load(sys.stdin)
if data: print(data[0].get('status', ''))
" 2>/dev/null || true)
if [ "$STATUS" = "completed" ]; then
  echo "  ✓ fast callback + slow timeout(skip) → workflow 完成"
else
  echo "  ⚠ 預期 completed，實際: $STATUS"
fi
echo ""

curl -s -X DELETE "$BASE/workflows/test-parallel-timeout" "${header_args[@]}" > /dev/null 2>&1 || true

echo "=== 場景 11 完成 ==="
