#!/usr/bin/env bash
# ============================================================
# External Step — 全場景驗證（依序執行所有測試）
# ============================================================
#
# 使用方式：
#   ./examples/test-external-all.sh [TETORA_URL]
#
# 包含場景（覆蓋 spec 45 場景）：
#   1.  OCR 文件辨識（single callback, open auth）
#   2.  Stripe 退款（ResponseMapping + condition 分支 x2）
#   3.  訂單流程（串聯多個 external step + output 傳遞）
#   4.  Timeout 行為（stop vs skip）
#   5.  Streaming callback（partial→final, accumulate, timeout 有部分結果）
#   6.  結構化欄位存取（子欄位 + 深層路徑 + 串接傳遞）
#   7.  Validation 規則（10 種不合法設定）
#   8.  認證模式（bearer / open / 無 token / 錯誤 token）
#   9.  冪等性 + 邊界（重複 callback / 空 body / 超大 body / __ 注入）
#   10. Condition + External 組合（then/else 分支 + fallback）
#   11. Parallel + External（並行等待 + 部分 timeout）
#   12. Content Type 變體（form-urlencoded + XML）
#   13. Cancel 等待中的 workflow
# ============================================================

set -euo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)"
BASE="${1:-http://localhost:7200}"

echo "╔══════════════════════════════════════════════╗"
echo "║  External Step — 全場景驗證（13 腳本 / 45 場景）║"
echo "║  Target: $BASE"
echo "╚══════════════════════════════════════════════╝"
echo ""

PASS=0
FAIL=0

run_test() {
  local name="$1"
  local script="$2"
  echo "────────────────────────────────────────"
  if bash "$script" "$BASE"; then
    echo "  ▶ $name: PASS"
    PASS=$((PASS + 1))
  else
    echo "  ▶ $name: FAIL"
    FAIL=$((FAIL + 1))
  fi
  echo ""
}

run_test "1.  OCR Pipeline"           "$DIR/test-external-ocr.sh"
run_test "2.  Stripe Refund"          "$DIR/test-external-stripe.sh"
run_test "3.  Order Fulfillment"      "$DIR/test-external-chained.sh"
run_test "4.  Timeout Behavior"       "$DIR/test-external-timeout.sh"
run_test "5.  Streaming Callback"     "$DIR/test-external-streaming.sh"
run_test "6.  Structured Fields"      "$DIR/test-external-structured.sh"
run_test "7.  Validation Rules"       "$DIR/test-external-validation.sh"
run_test "8.  Auth Modes"             "$DIR/test-external-auth.sh"
run_test "9.  Idempotency + Edge"     "$DIR/test-external-idempotent.sh"
run_test "10. Condition + External"   "$DIR/test-external-condition.sh"
run_test "11. Parallel + External"    "$DIR/test-external-parallel.sh"
run_test "12. Content Type Variants"  "$DIR/test-external-formdata.sh"
run_test "13. Cancel Workflow"        "$DIR/test-external-cancel.sh"

echo "════════════════════════════════════════════════"
echo "  Results: $PASS / 13 passed, $FAIL failed"
echo "════════════════════════════════════════════════"

if [ "$FAIL" -gt 0 ]; then
  exit 1
fi
