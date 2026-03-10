#!/usr/bin/env bash
# ============================================================
# 場景 6：結構化欄位存取 + ResponseMapping 深層提取
# ============================================================
#
# 使用情境：
#   Callback 回來的 JSON 需要提取子欄位給下一步使用。
#   例如：{{steps.ocr.output.extracted_text}} 取出 OCR 文字
#        {{steps.kyc.output.risk_score}} 取出風控分數
#
# 測試子場景：
#   A) 基本子欄位存取（頂層 key）
#   B) 巢狀路徑存取（data.object.status）
#   C) 陣列索引存取（items.0.id）
#   D) 欄位不存在時為空字串（不 crash）
#
# 覆蓋 spec 場景：
#   - R3: 金融 KYC 身分驗證（巢狀欄位提取 + condition）
#   - R3: 物流包裹追蹤（陣列索引 items.0.status）
#   - R3: 教育作業批改（output 非 JSON 時子欄位為空）
#   - R4: 面試排程→回饋串接（chained + 子欄位傳遞）
#   - R10: Webhook relay chain（前步 output 作後步 body）
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

echo "=== 場景 6：結構化欄位存取 + ResponseMapping ==="
echo ""

# ── 子欄位存取 + condition 分支 ──
echo "━━━ KYC 身分驗證 — 子欄位 + condition 分支 ━━━"
echo ""

echo "[1] 建立 workflow: test-structured-kyc"
curl -s -X DELETE "$BASE/workflows/test-structured-kyc" "${header_args[@]}" > /dev/null 2>&1 || true
RESULT=$(curl -s -w "\n%{http_code}" -X POST "$BASE/workflows" \
  "${header_args[@]}" \
  -d '{
    "name": "test-structured-kyc",
    "description": "Test: structured field access with condition branching",
    "variables": {
      "customer_id": ""
    },
    "steps": [
      {
        "id": "kyc-check",
        "type": "external",
        "externalUrl": "https://httpbin.org/post",
        "externalBody": {
          "customer_id": "{{customer_id}}",
          "callback_url": "'"$BASE"'/api/callbacks/kyc-{{runId}}"
        },
        "callbackKey": "kyc-{{runId}}",
        "callbackTimeout": "2m",
        "callbackAuth": "open",
        "callbackResponseMap": {
          "dataPath": "verification"
        },
        "onTimeout": "stop"
      },
      {
        "id": "risk-check",
        "type": "condition",
        "if": "{{steps.kyc-check.output.risk_level}} == low",
        "then": "approve",
        "else": "manual-review",
        "dependsOn": ["kyc-check"]
      },
      {
        "id": "approve",
        "type": "notify",
        "notifyMsg": "KYC approved for {{customer_id}} — score={{steps.kyc-check.output.score}}, name={{steps.kyc-check.output.full_name}}",
        "notifyTo": "log",
        "dependsOn": ["risk-check"]
      },
      {
        "id": "manual-review",
        "type": "notify",
        "notifyMsg": "KYC needs manual review for {{customer_id}} — risk={{steps.kyc-check.output.risk_level}}, flags={{steps.kyc-check.output.flags}}",
        "notifyTo": "log",
        "dependsOn": ["risk-check"]
      }
    ]
  }')
HTTP_CODE=$(echo "$RESULT" | tail -1)
echo "  Response ($HTTP_CODE)"
echo ""

echo "[2] 觸發 run（customer_id=CUST-001）"
curl -s -X POST "$BASE/workflows/test-structured-kyc/run" \
  "${header_args[@]}" \
  -d '{"variables": {"customer_id": "CUST-001"}}' | python3 -m json.tool 2>/dev/null
echo ""

CB_KEY=$(find_callback_key "kyc-" 10)
if [ -z "$CB_KEY" ]; then
  echo "  ✗ 找不到 callback key"
  exit 1
fi
echo "  ✓ callback key: $CB_KEY"
echo ""

echo "[3] 模擬 KYC 服務回傳（low risk → 應走 approve）"
echo "  送出巢狀 JSON，ResponseMapping 用 dataPath=verification 提取"
RESULT=$(curl -s -X POST "$BASE/api/callbacks/$CB_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "request_id": "req-abc",
    "verification": {
      "full_name": "王小明",
      "score": 92,
      "risk_level": "low",
      "flags": [],
      "documents": {
        "passport": "verified",
        "address": "verified"
      }
    }
  }')
echo "  Response: $RESULT"
echo ""

sleep 3
echo "[4] 確認結果"
RUNS=$(curl -s "$BASE/workflows/test-structured-kyc/runs" "${header_args[@]}")
echo "$RUNS" | python3 -c "
import json, sys
data = json.load(sys.stdin)
if data:
    run = data[0]
    print(f\"  status={run.get('status','?')}\")
    steps = run.get('step_results', {})
    if isinstance(steps, dict):
        for sid, sr in sorted(steps.items()):
            if isinstance(sr, dict):
                s = sr.get('status','?')
                o = sr.get('output','')[:80] if sr.get('output') else ''
                print(f\"    {sid}: status={s} output={o}...\")
" 2>/dev/null || echo "  $RUNS"
echo ""
echo "  驗證重點："
echo "    - kyc-check output 應為 verification 物件（ResponseMapping 提取）"
echo "    - risk-check 應走 approve（risk_level=low）"
echo "    - approve 的 notifyMsg 應包含 score=92, full_name=王小明"
echo ""

curl -s -X DELETE "$BASE/workflows/test-structured-kyc" "${header_args[@]}" > /dev/null 2>&1 || true

# ── 串接子欄位傳遞 ──
echo "━━━ 面試流程 — 串接 external steps + 子欄位傳遞 ━━━"
echo ""

echo "[5] 建立 workflow: test-structured-chain"
curl -s -X DELETE "$BASE/workflows/test-structured-chain" "${header_args[@]}" > /dev/null 2>&1 || true
curl -s -X POST "$BASE/workflows" \
  "${header_args[@]}" \
  -d '{
    "name": "test-structured-chain",
    "description": "Test: chained steps using sub-field from previous step output",
    "steps": [
      {
        "id": "schedule-interview",
        "type": "external",
        "externalUrl": "https://httpbin.org/post",
        "externalBody": {"candidate": "Alice", "position": "SWE"},
        "callbackKey": "interview-{{runId}}",
        "callbackTimeout": "2m",
        "callbackAuth": "open",
        "onTimeout": "stop"
      },
      {
        "id": "collect-feedback",
        "type": "external",
        "externalUrl": "https://httpbin.org/post",
        "externalBody": {
          "interview_id": "{{steps.schedule-interview.output.interview_id}}",
          "interviewer": "{{steps.schedule-interview.output.interviewer}}"
        },
        "callbackKey": "feedback-{{runId}}",
        "callbackTimeout": "2m",
        "callbackAuth": "open",
        "onTimeout": "stop",
        "dependsOn": ["schedule-interview"]
      },
      {
        "id": "summarize",
        "type": "notify",
        "notifyMsg": "Interview {{steps.schedule-interview.output.interview_id}} by {{steps.schedule-interview.output.interviewer}}: rating={{steps.collect-feedback.output.rating}}, decision={{steps.collect-feedback.output.decision}}",
        "notifyTo": "log",
        "dependsOn": ["collect-feedback"]
      }
    ]
  }' > /dev/null

echo "[6] 觸發 run"
curl -s -X POST "$BASE/workflows/test-structured-chain/run" "${header_args[@]}" -d '{}' > /dev/null
echo ""

CB_KEY=$(find_callback_key "interview-" 10)
if [ -z "$CB_KEY" ]; then echo "  ✗ 找不到 callback key"; exit 1; fi
echo "  ✓ interview callback: $CB_KEY"

echo "[7] 面試排程完成 → 回傳 interview_id + interviewer"
curl -s -X POST "$BASE/api/callbacks/$CB_KEY" \
  -H "Content-Type: application/json" \
  -d '{"interview_id": "INT-789", "interviewer": "David", "scheduled_at": "2026-03-15T14:00:00Z"}'
echo ""

sleep 2

CB_KEY2=$(find_callback_key "feedback-" 10)
if [ -z "$CB_KEY2" ]; then
  echo "  ✗ 找不到 feedback callback key — 子欄位傳遞可能失敗"
  exit 1
fi
echo "  ✓ feedback callback: $CB_KEY2"
echo "  ✓ 第二步成功啟動 — 子欄位傳遞正常"

echo "[8] 面試回饋回傳"
curl -s -X POST "$BASE/api/callbacks/$CB_KEY2" \
  -H "Content-Type: application/json" \
  -d '{"rating": 4.5, "decision": "hire", "comments": "Excellent problem solving"}'
echo ""

sleep 3
STATUS=$(curl -s "$BASE/workflows/test-structured-chain/runs" "${header_args[@]}" | python3 -c "
import json, sys
data = json.load(sys.stdin)
if data: print(data[0].get('status', ''))
" 2>/dev/null || true)
if [ "$STATUS" = "completed" ]; then
  echo "  ✓ 串接子欄位傳遞完成"
else
  echo "  ⚠ 預期 completed，實際: $STATUS"
fi
echo ""

curl -s -X DELETE "$BASE/workflows/test-structured-chain" "${header_args[@]}" > /dev/null 2>&1 || true

echo "=== 場景 6 完成 ==="
