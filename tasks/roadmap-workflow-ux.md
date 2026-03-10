# Workflow UX Roadmap — 讓一般使用者能用

> 日期：2026-03-11
> 目標：HR 等非技術使用者能在 Dashboard 上自助建立、觸發、監控 workflow
> 前置完成：external step 引擎 + 13 測試腳本 + trigger engine
> Review 1：2026-03-11（產品策略 review，補充 15 項缺漏）

---

## 現狀

| 能力 | 狀態 | 使用者體驗 |
|------|------|-----------|
| Workflow 定義 | ✅ JSON 檔案 | 只有開發者能寫 |
| Workflow 觸發 | ✅ API / CLI / Discord / Trigger Engine | 開發者設 config.json，使用者透過 Discord 或 webhook 間接觸發 |
| External Step | ✅ 引擎完成 | callback 機制可用，13 測試腳本覆蓋 45 場景 |
| Trigger 設定 | ✅ config.json workflowTriggers + API | `GET/POST /api/triggers` 已實作，webhook trigger `POST /api/triggers/webhook/{id}` 已可用 |
| Workflow Editor UI | 🚧 v2 開發中 | SVG node graph 基礎已有，需修 10 個 bug + UX |
| Run History UI | ⚠️ 基本版 | Dashboard 有 runs 列表 + DAG + timeline SVG，缺步驟級詳情 |
| Versioning | ✅ 後端完成 | `version.go` 有 snapshot/restore/diff，無 UI |
| Template | ✅ 43 個已有 | 7 內建 + 36 產業 template（`examples/templates/`），覆蓋 36 角色 × 12 產業，但無 Gallery UI |
| Dry Run | ⚠️ 測試級 | `workflow_dryrun_test.go` 存在，無使用者介面 |
| SSE 即時更新 | ✅ 已實作 | `sse.go` + broker 基礎建設完成 |
| Audit Log | ✅ 已實作 | `audit.go`，36 個檔案寫入，無專用 UI |
| Incoming Webhook | ✅ 已實作 | `incoming_webhook.go` 有 `triggerWebhookWorkflow()` |

---

## 使用者角色

### 通用企業角色

| 角色 | 需求 | 主要介面 | 適合度 |
|------|------|---------|--------|
| **開發者 / DevOps** | 設計 workflow、CI/CD、除錯 | CLI + Editor + API | HIGH |
| **HR 招募 / 人事** | 招募篩選、報到流程、績效考核 | Discord + Dashboard | HIGH |
| **法務 / 合規** | 合約審查、電子簽章、風險評估 | Dashboard | HIGH |
| **財務 / 會計** | 發票核對、付款確認、對帳 | Dashboard | HIGH |
| **客服主管** | 工單分類、退款審批、回覆草稿 | Discord + Dashboard | HIGH |
| **行銷 / 內容** | 內容產出、社群排程、活動管理 | Dashboard + Discord | HIGH |
| **採購** | 供應商評估、採購簽核、合約管理 | Dashboard | HIGH |
| **IT 管理員** | 帳號開通、權限管理、事件處理 | CLI + Dashboard | HIGH |
| **主管 / 決策者** | 審批、報表、成本控管 | Dashboard + Discord 通知 | MEDIUM |

### 產業專屬角色

| 角色 | 產業 | 核心 workflow | 適合度 |
|------|------|-------------|--------|
| **電商營運** | E-commerce | 訂單履行、庫存同步、退款處理 | HIGH |
| **業務 / AE** | SaaS / B2B | 提案產生、合約簽署、客戶研究 | HIGH |
| **客戶成功** | SaaS | 流失預警、QBR 準備、續約追蹤 | HIGH |
| **產品經理** | SaaS / Tech | 需求整理、Release Notes、使用者回饋分析 | HIGH |
| **保險核保 / 理賠** | Insurance | 理賠審查、拒賠申訴、風險評估 | HIGH |
| **醫療行政** | Healthcare | 保險預授權、檢驗追蹤、帳務申報 | HIGH |
| **房地產經紀** | Real Estate | 物件上架、電子簽約、客戶通知 | HIGH |
| **物業管理** | Real Estate | 租約續簽、維修派工、收款追蹤 | HIGH |
| **募款經理** | Non-profit | 補助申請、捐款管理、志工協調 | HIGH |
| **招生行政** | Education | 入學申請審查、成績處理、通知發送 | MEDIUM |
| **供應鏈分析** | Manufacturing | 異常偵測、補貨建議、供應商確認 | MEDIUM |
| **品管工程師** | Manufacturing | 缺陷報告、根因分析、檢驗回報 | MEDIUM |
| **物流調度員** | Logistics | 司機指派、出貨標籤、配送追蹤 | HIGH |
| **餐廳營運主管** | F&B | 日結對帳、差異分析、隔日備料 | HIGH |
| **銀行徵信專員** | Banking | 信用局查詢、DSCR 分析、徵信備忘錄 | HIGH |
| **政府採購專員** | Government | 標案合規檢查、評分矩陣、委員會報告 | HIGH |
| **藥廠法規專員** | Pharma | 法規詢問回覆、SME 審查、提交追蹤 | HIGH |
| **管理顧問** | Consulting | 訪談整合、簡報大綱、客戶交付 | MEDIUM |
| **媒體版權管理** | Media | 授權稽核、到期預警、續約請求 | MEDIUM |
| **工地安全主任** | Construction | 事故分級、法規通報、OSHA 報告 | HIGH |
| **旅館收益管理** | Hospitality | 競品比價、動態定價、通路推播 | HIGH |
| **電力公司客服** | Energy | 停電關聯、派工調度、主動通知 | HIGH |
| **零售門店店長** | Retail | 庫存預測、自動補貨、供應商 PO | MEDIUM |
| **臨床試驗協調** | Pharma | 受試者篩選、知情同意、EDC 記錄 | MEDIUM |
| **海關報關行** | Logistics | HS 分類、關稅計算、報關提交 | HIGH |
| **基金結算員** | Banking | 交易配對、Break 調查、結算指令 | HIGH |

---

## Roadmap

### P0：Workflow Editor 修復 + Quick Wins

> 前置：`tasks/spec-workflow-editor-v2.md` 已列 10 個問題

**Editor Bug Fix：**
- [ ] BUG-1: `fetchapi` undefined → 改用 `fetch()`
- [ ] BUG-2: 特定 workflow 載入失敗 → 加 error handling
- [ ] BUG-3: Raw JSON panel 不同步 → canvas 變更時自動更新
- [ ] UX-1: Pan (Space+drag) 壞了 → 加 middle-click pan
- [ ] UX-2: UI 太擠 → 加大 canvas/panel、fullscreen mode（按鈕已有）

**Step Type 補完：**
- [ ] Editor「+ Add Step」下拉加入 `external` 和 `handoff`（目前缺這兩個）
- [ ] External step property panel（URL、headers、body、callback 設定、auth mode、timeout）
- [ ] Handoff step property panel
- [ ] 驗證 external/handoff step 在 editor 裡能正確 save/load

**Quick Win — Version History Panel：**
- [ ] Editor 加「Version History」按鈕（`version.go` 後端已完成）
- [ ] 顯示版本列表 + diff 摘要
- [ ] 一鍵 rollback 到指定版本
- [ ] 這是非技術使用者的安全網 — 改壞了能復原

### P1：Trigger 設定 UI + Hot Reload

> 目標：使用者在 Dashboard 上設定 trigger，不需要改 config.json
> 注意：`GET /api/triggers` 和 `POST /api/triggers/webhook/{id}` 已實作

- [ ] Dashboard Triggers 頁面（列表 + 新增 + 編輯 + 刪除）
- [ ] 支援 3 種 trigger type：
  - cron：選時間 + timezone（視覺化 cron builder）
  - event：選事件類型（下拉，列出已知 SSE event types）
  - webhook：自動產生 URL + 顯示 curl 範例 + 可複製
- [ ] Variables mapping UI（key-value 編輯器，支援 `{{event_*}}` 提示）
- [ ] Cooldown 設定（duration picker）
- [ ] 啟用/停用 toggle
- [ ] Hot reload — CRUD trigger 不需重啟 daemon（`ReloadConfig` 或 trigger engine 動態更新）
- [ ] 補齊 API：`PUT /api/triggers/{name}`, `DELETE /api/triggers/{name}`
- [ ] Trigger 歷史（`workflow_trigger_runs` 表已有，加 UI 顯示最近觸發記錄）
- [ ] Webhook rate limiting（防 webhook storm，每個 trigger 獨立限速）

### P2：Workflow Run Detail + Error UX

> 目標：使用者能看到每一步的狀態和結果，出錯時知道怎麼辦

**Run Detail 頁面：**
- [ ] 點 run → 展開步驟列表（status、duration、output 可展開、error）
- [ ] Run timeline 視覺化（甘特圖式，顯示並行步驟）— 基礎 SVG 已有
- [ ] 即時更新（SSE）— `sse.go` broker 已就位，接上 step 事件

**External Step 特殊顯示：**
- [ ] Waiting 狀態 + elapsed time + timeout countdown
- [ ] Callback key（可複製）
- [ ] 手動 resolve 按鈕（填 JSON body → POST callback）
- [ ] Callback 歷史（顯示已收到的 callback 記錄）

**Error / Failure UX（關鍵）：**
- [ ] 失敗步驟紅色高亮 + error message 展示
- [ ] 「Retry from this step」按鈕 — 從失敗步驟重跑，不需整個 run 重來
- [ ] 「Skip and continue」按鈕 — 手動跳過失敗步驟
- [ ] Timeout 視覺提示（進度條 + 剩餘時間）
- [ ] External step 失敗時的引導：「外部服務無回應，你可以：①等待 ②手動送 callback ③跳過」
- [ ] 並行步驟部分失敗顯示：哪些成功、哪些失敗、能否只重試失敗的

**成本追蹤：**
- [ ] 每個 run 的 token 用量 + 預估費用
- [ ] 每步的成本breakdown
- [ ] Workflow 層級的累計成本

### P3：Template Gallery + Dry Run

> 目標：使用者從 template 開始，修改參數即可用

**Template Gallery：**
- [ ] Dashboard Template Gallery 頁面（卡片式，按角色/用途分類）
- [ ] 納入 43 個 workflow template（7 內建 + 36 產業）：

  **內建 workflow（7 個）：**
  - `standard-dev` → 開發者 / 標準開發 pipeline
  - `employee-onboarding` → HR / 新人報到
  - `content-publish` → 行銷 / 內容發布
  - `order-dispute` → 客服 / 訂單爭議
  - `workflow-external-basic` → OCR 文件辨識
  - `workflow-external-stripe` → 退款處理
  - `workflow-external-chained` → 訂單流程

  **產業 template（36 個，`examples/templates/`）— 覆蓋 36 角色 × 12 產業：**
  | Template | 角色 | 產業 | 步驟組合 |
  |----------|------|------|---------|
  | `tpl-resume-screening` | HR 招募 | 通用 | dispatch + condition + external + notify |
  | `tpl-contract-review` | 法務 | 通用 | dispatch + condition + external(DocuSign) + notify |
  | `tpl-support-triage` | 客服 | 通用 | dispatch + condition + external(退款) + notify |
  | `tpl-content-publish` | 行銷 | 通用 | dispatch + condition + external(CMS) + notify |
  | `tpl-invoice-reconciliation` | 財務 | 通用 | external(OCR) + dispatch + condition + notify |
  | `tpl-cicd-deploy` | DevOps | Tech | external(CI) + condition + external(staging) + notify |
  | `tpl-vendor-onboarding` | 採購 | 通用 | dispatch + condition + external(DocuSign+銀行+ERP) + notify |
  | `tpl-employee-onboarding-v2` | HR | 通用 | dispatch + external(帳號+薪資) + condition + notify |
  | `tpl-churn-intervention` | 客戶成功 | SaaS | external(health score) + condition + dispatch + notify |
  | `tpl-sales-proposal` | 業務 | B2B | dispatch + external(簽核+DocuSign) + condition + notify |
  | `tpl-insurance-claim` | 保險理賠 | Insurance | external(清算) + condition + dispatch + external(ERA) + notify |
  | `tpl-performance-review` | 主管/HR | 通用 | external(問卷) + dispatch + external(主管審核) + notify |
  | `tpl-grant-application` | 募款/教育 | NPO | dispatch + condition + external(審查+提交) + notify |
  | `tpl-real-estate-listing` | 房地產經紀 | Real Estate | dispatch + external(MLS+社群) + condition + notify |
  | `tpl-order-fulfillment` | 電商營運 | E-commerce | external(付款+庫存+倉儲+物流) + condition + notify |
  | `tpl-it-provisioning` | IT 管理員 | 通用 | external(AD+Email+Slack+IAM) + condition + notify |
  | `tpl-manager-approval` | 主管/決策者 | 通用 | dispatch + condition(自動審批) + external(48h) + notify |
  | `tpl-product-requirements` | 產品經理 | Tech | dispatch + condition + external(Linear) + dispatch + notify |
  | `tpl-healthcare-preauth` | 醫療行政 | Healthcare | dispatch + external(預授權 72h) + condition + dispatch(申訴) + notify |
  | `tpl-property-management` | 物業管理 | Real Estate | dispatch + external(租約 7d) + condition + external(維修) + notify |
  | `tpl-admissions-review` | 招生行政 | Education | external + dispatch(評分) + condition(三段) + external(委員會 5d) + notify |
  | `tpl-manufacturing-qc` | 品管/供應鏈 | Manufacturing | external(MES XML) + dispatch + condition(嚴重度) + external(SCAR 14d) + notify |
  | `tpl-freight-dispatch` | 物流調度員 | Logistics | external(TMS) + dispatch + external(司機 15m) + condition + delay(4h) + notify |
  | `tpl-restaurant-closing` | 餐廳營運主管 | F&B | external(POS) + external(現金) + dispatch + condition(差異) + notify |
  | `tpl-loan-credit-review` | 銀行徵信專員 | Banking | external(信用局) + external(財報) + dispatch(DSCR) + condition + dispatch(memo) + notify |
  | `tpl-gov-procurement` | 政府採購專員 | Government | external(標案) + dispatch(合規) + condition + dispatch(評分) + external + notify |
  | `tpl-pharma-regulatory-submission` | 藥廠法規專員 | Pharma | external(法規詢問) + dispatch + condition + delay(3d) + external(提交) + notify |
  | `tpl-consulting-deliverable` | 管理顧問 | Consulting | external(訪談) + dispatch(整合) + dispatch(簡報) + external + delay(24h) + condition |
  | `tpl-media-rights-clearance` | 媒體版權管理 | Media | external(授權庫) + dispatch(稽核) + condition + dispatch(續約函) + external + notify |
  | `tpl-site-safety-incident` | 工地安全主任 | Construction | external(事故) + dispatch(分級) + condition + external(OSHA) + delay(48h) + dispatch(報告) |
  | `tpl-hotel-rate-optimization` | 旅館收益管理 | Hospitality | external(PMS) + external(競品) + dispatch(定價) + condition + external(推播) + notify |
  | `tpl-utility-outage-response` | 電力公司客服 | Energy | external(報修) + external(SCADA) + dispatch + condition + external(派工) + delay + notify |
  | `tpl-retail-replenishment` | 零售門店店長 | Retail | external(庫存) + dispatch(預測) + external(供應商) + dispatch(PO) + condition + notify |
  | `tpl-clinical-trial-screening` | 臨床試驗協調 | Pharma | external(EMR) + dispatch(篩選) + condition + external(EDC) + dispatch(通知) + notify |
  | `tpl-customs-clearance` | 海關報關行 | Logistics | external(發票) + dispatch(HS分類) + condition + external(關稅) + dispatch(報關) + condition |
  | `tpl-fund-trade-settlement` | 基金結算員 | Banking | external(OMS) + external(SWIFT) + dispatch(配對) + condition + dispatch(Break) + external + notify |

- [ ] 「Use Template」→ 複製為新 workflow → 進 editor 修改
- [ ] Template 說明卡（使用情境、需要設定的 variables、外部服務需求、預估成本）
- [ ] 按產業分類篩選（通用 / Tech / SaaS / B2B / E-commerce / Banking / Insurance / Healthcare / Real Estate / Manufacturing / Logistics / F&B / Retail / Government / Pharma / Consulting / Media / Construction / Hospitality / Energy / Education / NPO）

**Dry Run / Test Mode：**
- [ ] Editor 加「Test Run」按鈕（`workflow_dryrun_test.go` 有基礎）
- [ ] Dry run 模式：不實際 POST 到外部、不實際 dispatch agent，但走完整 DAG 邏輯
- [ ] 顯示每步會做什麼（resolved template、目標 URL、預估 timeout）
- [ ] 驗證 variables 是否完整（缺少的 variable 標紅提示）

**Secrets Management（外部服務認證）：**
- [ ] Variables 中的 `{{env.API_KEY}}` 類型 → 在 Dashboard 設定（masked input）
- [ ] 環境變數管理頁面（新增/編輯/刪除，值 masked）
- [ ] 不在 workflow JSON 裡存明文 key

### P4：權限與存取控制

> 目標：正式多人使用的前提 — 不同使用者看到/能做的不同
> ⚠️ 此項優先於 Discord NL（組織部署的前置條件）

- [ ] Dashboard 登入角色：
  - **admin** — 全部權限
  - **operator** — 可觸發 workflow、看 run detail、設 trigger
  - **viewer** — 只能看 run 結果和狀態
- [ ] Workflow 權限標記（owner、allowed_roles）
- [ ] Trigger 權限（誰能建/改/刪 trigger）
- [ ] Audit log UI — 誰在什麼時候觸發了什麼（`audit.go` 後端已有）
- [ ] Callback URL 安全：
  - 支援 callback key rotation（定期更換）
  - 支援 revoke specific callback（手動作廢）
- [ ] Budget limit per workflow（每月/每次上限）

### P5：Discord 自然語言觸發

> 目標：HR 在 Discord 說「新人報到：王小明」就能觸發 workflow

- [ ] Intent detection — 從使用者訊息判斷要觸發哪個 workflow
- [ ] Variable extraction — 從訊息提取 variables（姓名、部門等）
- [ ] 確認 prompt —「要啟動新人報到流程嗎？王小明 / 工程部 / 4月1日」→ 使用者確認按鈕
- [ ] 進度通知 — workflow 每步完成時在 Discord 更新（已有 `discord_progress.go`）
- [ ] 結果摘要 — workflow 完成時送完整結果
- [ ] 錯誤通知 — 步驟失敗時在 Discord 提供操作選項（retry / skip / escalate）

### P6：進階運營功能

> 目標：長期運營的穩定性和可觀測性

**長時間 Workflow 管理：**
- [ ] Dashboard「Stuck Workflows」視圖 — 等待超過 N 小時的 run
- [ ] 升級規則：等待超過 X 時間 → 自動通知負責人
- [ ] SLA 追蹤：每步/每 workflow 的平均完成時間 vs 目標

**Workflow 治理：**
- [ ] 循環觸發偵測（Workflow A 觸發 B 觸發 A → 阻止）
- [ ] Concurrent run 限制（同一 workflow 最多 N 個同時執行）
- [ ] Workflow 停用/啟用（暫停所有 trigger）

**分析與報表：**
- [ ] Workflow 執行統計（成功率、平均耗時、成本趨勢）
- [ ] 匯出報表（CSV / PDF）
- [ ] 定期摘要郵件（每週 workflow 執行報告）

---

## 里程碑

| 里程碑 | 內容 | 效果 | 預估 |
|--------|------|------|------|
| **M1** | P0 | Editor 可用 + version rollback 安全網 | 短期 |
| **M2** | P1 + P2 | 完整觸發 + 監控 + 錯誤處理，開發者可自用 | 中期 |
| **M3** | P3 | Template + Dry Run，非技術使用者可上手 | 中期 |
| **M4** | P4 | 權限控管，可開放給組織內多人使用 | 中期 |
| **M5** | P5 | Discord NL 觸發，最自然的使用體驗 | 長期 |
| **M6** | P6 | 長期運營穩定性 | 長期 |

---

## 使用者旅程

### 開發者（M2 後）
```
Editor 建 workflow → Test Run 驗證 → Save
→ Triggers 頁面 → 新增 trigger → 看 Run Detail 確認
→ 出錯 → Run Detail 看 error → Retry from step → 修 workflow → Version History 對比
```

### HR / 非技術使用者（M3 後）
```
Dashboard → Template Gallery → 選「新人報到」→ 填 variables → Save
→ Triggers 頁面 → 設 webhook trigger → 複製 URL 給 HR 系統
→ 日常：HR 系統建新人 → 自動觸發 → Dashboard 看進度 → Discord 收通知
→ 出錯 → Run Detail 看引導「外部服務無回應，你可以...」→ 點「手動 resolve」
```

### HR（M5 後）
```
Discord：「@Tetora 新人報到：王小明，工程部，4/1」
→ Tetora 確認按鈕 → HR 點確認 → 自動跑流程
→ Discord 即時更新進度 → 完成通知
→ 失敗 → Discord 提供 retry/skip 按鈕
```

### 主管（M4 後）
```
Dashboard → 看 Workflow 執行統計（成功率、成本）
→ Audit Log 看誰觸發了什麼
→ 設 Budget Limit 控管成本
→ 收到升級通知（stuck workflow）→ 進 Run Detail 處理
```

---

## 競品對照

| 功能 | Zapier | n8n | Make.com | Tetora（M3 後）|
|------|--------|-----|----------|----------------|
| 視覺化 Editor | ✅ | ✅ | ✅ | ✅ |
| Template Gallery | ✅ | ✅ | ✅ | ✅ |
| Version History | ✅ | ✅ | ❌ | ✅（version.go） |
| Trigger 設定 UI | ✅ | ✅ | ✅ | ✅ |
| Error Recovery UI | ✅ | ⚠️ | ✅ | ✅（retry/skip） |
| Dry Run / Test | ⚠️ | ✅ | ⚠️ | ✅ |
| AI Agent 整合 | ❌ | ❌ | ❌ | ✅（核心優勢）|
| Async Callback | ❌ | ⚠️ | ⚠️ | ✅（核心優勢）|
| Discord/Slack Bot | ❌ | ❌ | ❌ | ✅（核心優勢）|
| NL 觸發 | ❌ | ❌ | ❌ | ✅（M5） |
| 多 Agent 協作 | ❌ | ❌ | ❌ | ✅（核心優勢）|
