#!/usr/bin/env bash
# ============================================================
# 場景 1：OCR 文件辨識 — External Step 驗證腳本
# ============================================================
#
# 使用情境：
#   使用者有一張收據圖片，想用 OCR 服務辨識文字。
#   OCR 服務處理需要 10 秒 ~ 3 分鐘，完成後會回呼 Tetora。
#
# 操作流程：
#   1. 使用者建立 workflow（定義 OCR step + notify step）
#   2. 使用者觸發 run，帶入 image_url
#   3. Tetora POST 到 OCR 服務（本測試用 callbackAuth=open 跳過實際 POST）
#   4. OCR 服務處理完成，POST callback 到 Tetora
#   5. Workflow 恢復 → 送通知
#
# 使用方式：
#   ./examples/test-external-ocr.sh [TETORA_URL]
#   預設 TETORA_URL=http://localhost:7200
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

echo "=== 場景 1：OCR 文件辨識 ==="
echo ""

# ── Step 1: 建立 workflow ──
echo "[1/5] 建立 workflow: test-ocr-pipeline"
RESULT=$(curl -s -w "\n%{http_code}" -X POST "$BASE/workflows" \
  "${header_args[@]}" \
  -d '{
    "name": "test-ocr-pipeline",
    "description": "Test: OCR external step with callback",
    "variables": {
      "image_url": ""
    },
    "steps": [
      {
        "id": "send-ocr",
        "type": "external",
        "externalUrl": "https://httpbin.org/post",
        "externalBody": {
          "image_url": "{{image_url}}",
          "language": "zh-TW",
          "callback_url": "'"$BASE"'/api/callbacks/ocr-{{runId}}"
        },
        "callbackKey": "ocr-{{runId}}",
        "callbackTimeout": "2m",
        "callbackAuth": "open",
        "onTimeout": "stop"
      },
      {
        "id": "notify-result",
        "type": "notify",
        "notifyMsg": "OCR result: {{steps.send-ocr.output}}",
        "notifyTo": "log",
        "dependsOn": ["send-ocr"]
      }
    ]
  }')
HTTP_CODE=$(echo "$RESULT" | tail -1)
BODY=$(echo "$RESULT" | sed '$d')
echo "  Response ($HTTP_CODE): $BODY"
if [ "$HTTP_CODE" != "201" ] && [ "$HTTP_CODE" != "200" ]; then
  echo "  ⚠ Workflow may already exist, continuing..."
fi
echo ""

# ── Step 2: 觸發 run ──
echo "[2/5] 觸發 workflow run"
RESULT=$(curl -s -w "\n%{http_code}" -X POST "$BASE/workflows/test-ocr-pipeline/run" \
  "${header_args[@]}" \
  -d '{
    "variables": {
      "image_url": "https://example.com/receipt.jpg"
    }
  }')
HTTP_CODE=$(echo "$RESULT" | tail -1)
BODY=$(echo "$RESULT" | sed '$d')
echo "  Response ($HTTP_CODE): $BODY"
echo ""

# ── Step 3: 確認 callback 已註冊 ──
echo "[3/5] 等待 callback 註冊（2 秒）..."
sleep 2
echo "  查詢 pending callbacks:"
CALLBACKS=$(curl -s "$BASE/api/callbacks" "${header_args[@]}")
echo "  $CALLBACKS" | python3 -m json.tool 2>/dev/null || echo "  $CALLBACKS"

# 找出 callback key
CB_KEY=$(echo "$CALLBACKS" | python3 -c "
import json, sys
data = json.load(sys.stdin)
for cb in data.get('callbacks', []):
    key = cb.get('key', '')
    if key.startswith('ocr-'):
        print(key)
        break
" 2>/dev/null || true)

if [ -z "$CB_KEY" ]; then
  echo "  ✗ 找不到 OCR callback key，可能 workflow 還未到 external step"
  echo "  再等 3 秒..."
  sleep 3
  CALLBACKS=$(curl -s "$BASE/api/callbacks" "${header_args[@]}")
  CB_KEY=$(echo "$CALLBACKS" | python3 -c "
import json, sys
data = json.load(sys.stdin)
for cb in data.get('callbacks', []):
    key = cb.get('key', '')
    if key.startswith('ocr-'):
        print(key)
        break
" 2>/dev/null || true)
fi

if [ -z "$CB_KEY" ]; then
  echo "  ✗ 仍找不到 callback key，中止測試"
  exit 1
fi
echo ""
echo "  ✓ 找到 callback key: $CB_KEY"
echo ""

# ── Step 4: 模擬 OCR 服務回呼 ──
echo "[4/5] 模擬 OCR 服務回傳結果"
RESULT=$(curl -s -w "\n%{http_code}" -X POST "$BASE/api/callbacks/$CB_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "text": "收據金額：$1,250",
    "confidence": 0.95,
    "lines": ["品項A $500", "品項B $750"]
  }')
HTTP_CODE=$(echo "$RESULT" | tail -1)
BODY=$(echo "$RESULT" | sed '$d')
echo "  Response ($HTTP_CODE): $BODY"
echo ""

# ── Step 5: 確認 workflow 完成 ──
echo "[5/5] 等待 workflow 完成（3 秒）..."
sleep 3
echo "  查詢最近 workflow runs:"
RUNS=$(curl -s "$BASE/workflows/test-ocr-pipeline/runs" "${header_args[@]}")
echo "$RUNS" | python3 -c "
import json, sys
data = json.load(sys.stdin)
for run in data[:3]:
    print(f\"  run_id={run.get('run_id','?')[:8]}... status={run.get('status','?')}\")
" 2>/dev/null || echo "  $RUNS"
echo ""

# ── Cleanup ──
echo "[cleanup] 刪除測試 workflow"
curl -s -X DELETE "$BASE/workflows/test-ocr-pipeline" "${header_args[@]}" | python3 -m json.tool 2>/dev/null || true
echo ""
echo "=== 場景 1 完成 ==="
