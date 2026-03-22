---
title: "Tetora v2.1.0 — 大規模程式碼整合 + Workflow Engine"
lang: zh-TW
date: "2026-03-18"
tag: release
readTime: "約 5 分鐘"
excerpt: "256 個檔案整合為精簡核心。新 Workflow Engine 支援 DAG 與 Template Marketplace。"
---

Tetora v2.1.0 是一次重大更新。這個版本的核心主題有兩個：**程式碼架構大整理**與**新功能落地**。

從使用者角度看，你會得到更穩定的執行環境、更快的疊代週期，以及等待已久的 Workflow Engine 和 Template Marketplace。從開發者角度看，這是 Tetora 從快速原型走向長期可維護產品的關鍵一步。

> **一句話總結：** root 原始碼檔案從 28 個整合為 9 個，測試檔案從 111 個整合為 22 個，整個 repo 從 256+ 檔案一路壓縮到現在的精簡結構——同時新增了 Workflow Engine、Template Marketplace 和多項 Dashboard 改進。

## 程式碼整合：256 檔案 → 精簡核心

這次整合歷經多輪重構，最終成果如下：

| 指標 | 整合前 | 整合後 |
|---|---|---|
| Repo 總檔案數 | 256+ | ~73（持續進行中） |
| Root source files | 28 | 9 |
| Test files | 111 | 22 |

9 個 root 檔案按領域劃分：

- `main.go` — 程式入口、指令路由、啟動流程
- `http.go` — HTTP server、API 路由、Dashboard handler
- `discord.go` — Discord gateway、訊息處理、終端機橋接
- `dispatch.go` — 任務調度、TaskBoard、並行控制
- `workflow.go` — Workflow Engine、DAG 執行、步驟管理
- `wire.go` — 跨模組配線、初始化、依賴注入
- `tool.go` — 工具系統、MCP 整合、能力管理
- `signal_unix.go` / `signal_windows.go` — 平台信號處理

大量業務邏輯同步遷移到 `internal/` 子套件：`internal/cron`、`internal/dispatch`、`internal/workflow`、`internal/taskboard`、`internal/reflection` 等，讓 root 層保持薄薄的一層協調邏輯。

### 為什麼這件事重要？

過去 root 層有超過 100 個檔案，每個功能散落各處，新進貢獻者光是找到正確的檔案就要花相當多時間。整合後：

- **更容易維護**——修改一個功能，你知道去哪裡找
- **新貢獻者更快上手**——9 個入口點，而不是 28 個
- **IDE 導航更清晰**——go to definition 不再在茫茫檔案海中迷路
- **編譯更快**——減少不必要的套件邊界和 import 鏈

## Workflow Engine

Workflow Engine 是 v2.1.0 最核心的新功能。它讓你可以用 YAML 描述多步驟的 AI 工作流，Tetora 負責執行、錯誤處理、狀態追蹤。

### DAG-based Pipeline

工作流以有向無環圖（DAG）結構定義，支援：

- **條件分支**——根據前一步驟的輸出決定走哪條路
- **並行步驟**——沒有相依關係的步驟同時執行，縮短總時間
- **Retry 機制**——步驟失敗後自動重試，可設定次數與退避策略

```yaml
name: content-pipeline
steps:
  - id: research
    agent: hisui
    prompt: "研究主題：{{input.topic}}"
  - id: draft
    agent: kokuyou
    depends_on: [research]
    prompt: "根據研究結果撰寫初稿"
  - id: review
    agent: ruri
    depends_on: [draft]
    condition: "{{draft.word_count}} > 500"
```

### Dynamic Model Routing

Workflow Engine 根據任務複雜度自動選擇模型：

- 簡單格式化、摘要任務 → **Haiku**（快速、低成本）
- 一般推理、文案撰寫 → **Sonnet**（預設）
- 複雜分析、多步驟規劃 → **Opus**（最高能力）

你也可以在 YAML 中明確指定，或讓路由器根據 prompt 長度和關鍵字自動判斷。

### Dashboard DAG 視覺化

執行中的 Workflow 在 Dashboard 上以節點圖形式呈現：已完成步驟顯示綠色、執行中顯示紫色動畫、等待中顯示灰色、失敗顯示紅色。你可以即時看到整個 Pipeline 的進度，不需要看 log。

## Template Marketplace

Template Marketplace 讓工作流模板可以被分享、瀏覽、一鍵匯入。這是 Tetora 從個人工具走向生態系的第一步。

### Store Tab

Dashboard 新增 Store 分頁，提供：

- **分類瀏覽**——按領域篩選（行銷、工程、財務、研究等）
- **全文搜尋**——在模板名稱和描述中搜尋
- **精選展示**——官方推薦的高品質模板
- **一鍵匯入**——點擊即匯入到本地 workspace

### Capabilities Tab

另一個新分頁 Capabilities，把你的 Tetora 實例擁有的所有能力集中展示：

- **Tools**——可用的 MCP 工具清單
- **Skills**——已定義的 Skill 指令
- **Workflows**——本地 Workflow 模板
- **Templates**——Agent prompt 模板

### CLI Import / Export

不只是 UI，Store 功能也完整支援 CLI 操作：

```bash
tetora workflow export my-pipeline   # 匯出為可分享的 YAML
tetora workflow create from-store    # 從 Store 匯入模板
tetora workflow list                  # 列出所有本地 Workflow
```

匯出的 YAML 可以直接貼到 GitHub Gist 或 Tetora Store 上分享給社群。

## TaskBoard & Dispatch 改進

TaskBoard 和 Dispatch 層有多項重要改進，提升了多 agent 並行工作的穩定性和可觀測性。

### 可配置並行 Slot + Slot Pressure

你現在可以在設定檔中指定最大並行 slot 數，以及 slot pressure 閾值。當系統負載超過閾值，新任務會自動排隊而不是強行插入，避免 agent 互相競爭資源：

```json
{
  "dispatch": {
    "maxSlots": 4,
    "slotPressureThreshold": 0.8
  }
}
```

### Partial-Done 狀態

長任務現在支援 `partial-done` 中間狀態。Agent 可以在完成部分工作後回報進度，TaskBoard 上會顯示完成百分比，讓你知道任務正在推進而不是卡住了。

### Worktree 資料保護

多個 agent 使用 Git worktree 並行開發時，現在有明確的資料隔離保護。每個 agent 的工作目錄獨立，不會發生意外覆寫或 merge conflict 污染其他 agent 的狀態。

### GitLab MR 支援

除了 GitHub PR，現在也支援 GitLab Merge Request 工作流。`tetora pr create` 指令會自動偵測遠端 remote 類型，分別呼叫 GitHub CLI 或 GitLab CLI 建立 MR。

## 安裝 / 升級

### 全新安裝

```bash
curl -fsSL https://tetora.dev/install.sh | bash
```

單一執行檔，零外部依賴。macOS、Linux、Windows 全平台支援。

### 從舊版升級

```bash
tetora upgrade
```

升級指令會自動下載最新版本、替換執行檔、重啟 daemon。升級過程中不會中斷正在執行的任務。

> **注意：** 升級前建議先確認沒有長時間執行的 Workflow 正在進行中。執行 `tetora status` 查看當前活躍的任務。

## 下一步：v2.2 規劃

v2.1.0 落地後，開發重心將轉向 v2.2 的兩個主題模組：

### Financial Module

針對個人和小型企業的財務自動化：收支追蹤、報表生成、預算監控。計劃整合常見的記帳 API（freee、Money Forward 等）。

### Nutrition Module

健康與飲食追蹤：餐點記錄、營養分析、目標設定。Claude 作為營養顧問，根據你的飲食習慣提供建議。

這兩個模組都會以 Workflow 模板的形式發布到 Store，讓你可以直接匯入使用，不需要從頭配置。

## 立即升級到 v2.1.0

單一執行檔，零依賴。macOS / Linux / Windows 全平台支援。

```bash
tetora upgrade
```

[查看 Release Notes](https://github.com/TakumaLee/Tetora/releases)
