# [BUG] Worktree 產出丟失 — git 失敗時 cleanup 仍執行導致資料永久遺失

> 來源事件：[Goldenfish Dispatch 全滅 (2026-03-11)](~/.tetora/workspace/memory/incident_20260312_goldenfish_dispatch.md)
> 影響：5 Phase dispatch 全部產出丟失，花費 $19.5 全白做

---

## 問題總覽

Dispatch 完成後 worktree merge/commit 失敗時，程式碼仍標 task 為 `done` 且 `defer cleanup` 強制刪除 worktree 目錄，導致 agent 寫的程式碼永久丟失且無法復原。

---

## BUG 1：Worktree cleanup 不管 merge 成敗一律執行

**位置**: `taskboard_git.go:154-162`

```go
defer func() {
    if err := d.worktreeMgr.Remove(projectWorkdir, worktreeDir); err != nil {
        logWarn("worktree: cleanup failed", ...)
    } else {
        logInfo("worktree: cleaned up", ...)
    }
}()
```

**問題**：`defer` 無條件執行。merge 失敗時 log 寫了 `Changes preserved on branch task/{id}`，但 cleanup 馬上刪掉了那個 branch。承諾保留但實際上刪了。

**修復方向**：
- merge 失敗時跳過 cleanup，保留 worktree 目錄和 branch
- 或至少保留 branch（只刪 worktree 目錄，不 `git branch -D`）
- 在 task comment 留下 worktree path 供人工 recovery

---

## BUG 2：`postTaskWorkspaceGit` 失敗不影響 task status

**位置**: `taskboard_git.go:36-38`

```go
if out, err := exec.Command("git", "-C", wsDir, "add", "-A").CombinedOutput(); err != nil {
    logWarn("postTaskWorkspaceGit: git add failed", "task", t.ID, "error", string(out))
    return  // ← 靜默 return，task 仍為 done
}
```

**問題**：git add 或 commit 失敗只 log warning 就 return，不修改 task status。外面看到的是 `done`，但實際產出沒有被 commit。

**修復方向**：
- git 操作失敗 → task status 改為新增的 `partial-done`（或至少在 task comment 標記 `[WARNING] workspace git commit failed`）
- 讓 triage/health check 能撈到這類 task

---

## BUG 3：Dispatch 前不檢查 workspace git health

**位置**: `taskboard_dispatch.go:466` 附近（worktree 建立前）

**問題**：沒有任何 pre-dispatch git health check。如果 `index.lock` stale 或 object store 損壞：
- worktree 可能建立成功（它建自己的 branch）
- 但 merge 回 main 時必定失敗
- 然後 cleanup 刪掉 worktree → 白做

**這次事件的主因**：`index.lock` 從 3/7 就卡著（5 天），期間所有 dispatch 的 git 操作都會失敗。

**修復方向**：
- dispatch 前檢查 `$workspaceDir/.git/index.lock` 是否存在
  - 存在且 mtime > 1 小時 → 自動刪除 stale lock + log
  - 存在且 mtime < 1 小時 → 等待或跳過此 task
- 可選：定期跑 `git fsck` 簡易檢查（代價不大）

---

## BUG 4：Auto-review 在 execution error 時靜默跳過

**位置**: `taskboard_review.go:232-233`

```go
if result.Status != "success" {
    return reviewResult{Verdict: reviewApprove, Comment: "review skipped (execution error)", ...}
}
```

**問題**：execution error 時 review verdict 回傳 `approve`。這代表：
1. 品質關卡完全跳過
2. Task 可能被標為 `done`
3. 外面看不出 review 沒有實際執行

這次 Phase 2b/3/4 都是 `review skipped (execution error)` 後直接標 done。

**修復方向**：
- execution error 時 verdict 應該是 `skip`（新增）或 `escalate`，不能是 `approve`
- 至少在 task comment 明確標記 `[WARNING] review skipped due to execution error`
- 考慮新增 review verdict：`skip`（跟 approve 區別開）

---

## BUG 5：沒有 `partial-done` 或 `partial-fail` status

**位置**: `taskboard.go:390`

```go
validStatuses := []string{"idea", "needs-thought", "backlog", "todo", "doing", "review", "done", "failed"}
```

**問題**：只有 `done` 和 `failed`。現實情況是 agent 執行成功但後續步驟（git commit/merge/review）失敗，需要一個中間狀態。

**修復方向**：
- 新增 `partial-done` status：agent 完成但 post-processing 有問題
- triage 和 health check 可以特別關注這個狀態
- 或用 flag 機制：在 `done` task 上加 `[flag:git-failed]` tag

---

## 重現步驟

```bash
# 1. 製造 stale index.lock
touch ~/.tetora/workspace/.git/index.lock

# 2. Dispatch 任何 task 給 kokuyou
tetora dispatch --role kokuyou "寫一個 hello world"

# 3. 觀察結果：
#    - agent 在 worktree 裡寫了程式碼 ✓
#    - postTaskWorkspaceGit git add 失敗（index.lock）
#    - merge 失敗
#    - worktree 被 defer cleanup 刪除
#    - task 標為 done
#    - 程式碼消失
```

---

## 建議修復優先序

| 優先 | 項目 | 理由 |
|------|------|------|
| **P0** | BUG 1: merge 失敗時保留 worktree | 直接防止資料丟失 |
| **P0** | BUG 3: dispatch 前檢查 index.lock | 防止已知會失敗的 dispatch 繼續跑 |
| **P1** | BUG 2: git 失敗時標記 task status | 讓問題可被發現 |
| **P1** | BUG 4: review skip 不能當 approve | 品質關卡誠實性 |
| **P2** | BUG 5: partial-done status | 狀態機完整性 |
| **P1** | FEAT 1: dispatch 自動注入 lessons.md | agent 學習機制基礎 |

---

## FEAT 1：Dispatch 自動注入 `agents/{name}/lessons.md`

**背景**：團隊已建立自我學習協定（`TEAM-RULEBOOK.md` Rule 11），每個 agent 在 `agents/{name}/lessons.md` 維護經驗教訓。但目前 dispatch 不會自動注入這個檔案。

### 現況

**位置**: `prompt_tier.go` + `dispatch_tools.go`

- `buildTieredPrompt()` 注入 SOUL.md、workspace rules、knowledge dir
- `injectWorkspaceContent()` 注入 `workspace/rules/` 目錄
- **Claude Code provider 跳過 workspace injection**（因為它能自己讀檔案）
- `agents/{name}/lessons.md` 不在任何自動注入路徑上

### 需求

1. **非 claude-code provider**：dispatch 時自動將 `agents/{name}/lessons.md` 內容附加到 agent prompt（接在 SOUL.md 之後）
2. **Claude Code provider**：在 prompt 裡加一行提醒「任務開始前請讀取 `agents/{你的名字}/lessons.md`」（因為它能自己讀檔案）
3. **寫回機制**：agent 在 worktree 裡更新 lessons.md 後，`postTaskWorkspaceGit` 應確保這個檔案被 commit
4. **大小限制**：lessons.md 超過 4KB 時只注入最新 10 條（避免浪費 token）

### 建議實作位置

- `prompt_tier.go` 的 soul injection 之後（約 line 50），加入 lessons injection
- 或在 `injectWorkspaceContent()` 裡加入 agent-specific 檔案的注入邏輯
