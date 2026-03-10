# External Step — 規格書

> 狀態：定案 ✅（45 場景通過，連續 3 輪無新問題）
> 日期：2026-03-10
> Review 1：2026-03-10（25 項，5 Critical 已修正）
> Review 2：2026-03-10（5 場景驗證，5 個關鍵缺口已補）
> Review 3：2026-03-10（5 新場景驗證，6 Critical 已修正）
> Review 4：2026-03-10（5 新場景驗證，5 Critical + 3 Warning 已修正）
> Review 5：2026-03-10（5 新場景驗證，2 Critical + 4 Warning 已修正）
> Review 6：2026-03-10（5 新場景驗證，2 Critical 已修正）+ 整合重寫
> Review 7：2026-03-10（5 新場景驗證，2 Critical + 3 Warning）+ 核心流程重設計 + 整合重寫 v2

## 目標

新增 `type: "external"` step，讓 workflow 可以：
1. 發 HTTP request 到外部服務（支援 JSON / XML / form-encoded）
2. 暫停執行，等待外部 callback（支援 single / streaming 模式）
3. Callback 進來後恢復執行，把回傳資料當作 step output
4. 支援結構化欄位存取（`{{steps.id.output.field}}`）

適用場景：OCR、支付、人工審批、CI/CD 觸發、醫療檢驗、法律審查、製造品檢、任何第三方 API 整合。

---

## WorkflowStep 新增欄位

```go
// workflow.go — WorkflowStep 新增
type WorkflowStep struct {
    // ... 既有欄位 ...

    // External step fields
    ExternalURL         string            `json:"externalUrl,omitempty"`         // POST 目標 URL
    ExternalHeaders     map[string]string `json:"externalHeaders,omitempty"`     // 自訂 headers（支援 template vars）
    ExternalBody        map[string]string `json:"externalBody,omitempty"`        // request body KV（支援 template vars）
    ExternalRawBody     string            `json:"externalRawBody,omitempty"`     // 原始 body（XML / 自訂格式，與 ExternalBody 二擇一）
    ExternalContentType string            `json:"externalContentType,omitempty"` // 預設 application/json
    CallbackKey         string            `json:"callbackKey,omitempty"`         // callback 配對 key（支援 template vars，建議包含 {{runId}}）
    CallbackTimeout     string            `json:"callbackTimeout,omitempty"`     // 等待 timeout（預設 1h，上限 30d）
    CallbackMode        string            `json:"callbackMode,omitempty"`        // "single"（預設）或 "streaming"
    CallbackAccumulate  bool              `json:"callbackAccumulate,omitempty"`  // streaming: true=累積所有結果為 JSON array, false=只保留最後一筆
    CallbackAuth        string            `json:"callbackAuth,omitempty"`        // "bearer"(預設), "open"(不驗 token), "signature"(Phase 3 HMAC)
    CallbackContentType string            `json:"callbackContentType,omitempty"` // callback 回來的格式，預設 application/json
    CallbackResponseMap *ResponseMapping  `json:"callbackResponseMap,omitempty"` // 從 webhook body 提取 status/data 的路徑
    OnTimeout           string            `json:"onTimeout,omitempty"`           // timeout 行為：stop / skip
}

// ResponseMapping — 從任意 webhook body 提取標準化的 status 和 data
type ResponseMapping struct {
    StatusPath string `json:"statusPath,omitempty"` // JSONPath-like: "data.object.status"
    DataPath   string `json:"dataPath,omitempty"`   // JSONPath-like: "data.object"
    DonePath   string `json:"donePath,omitempty"`   // streaming 模式：判斷是否為最終 callback 的路徑
    DoneValue  string `json:"doneValue,omitempty"`  // streaming 模式：DonePath 的值等於此時視為完成
}
```

### Body 發送模式

| 模式 | 欄位 | Content-Type | 用途 |
|------|------|-------------|------|
| KV JSON（預設）| `externalBody` | `application/json` | 大多數 REST API |
| Raw body | `externalRawBody` | 由 `externalContentType` 指定 | XML / SOAP / 自訂格式 |

兩者互斥，validation 時檢查。

### JSON 範例

**標準 JSON API（OCR）：**
```json
{
  "id": "send-ocr",
  "type": "external",
  "externalUrl": "https://ocr-service.com/api/recognize",
  "externalHeaders": {
    "Authorization": "Bearer {{env.OCR_API_KEY}}"
  },
  "externalBody": {
    "image_url": "{{image_url}}",
    "language": "zh-TW"
  },
  "callbackKey": "ocr-{{runId}}-send-ocr",
  "callbackTimeout": "5m",
  "onTimeout": "stop",
  "dependsOn": ["classify"]
}
```

**Stripe webhook（需 ResponseMapping）：**
```json
{
  "id": "stripe-refund",
  "type": "external",
  "externalUrl": "https://api.stripe.com/v1/refunds",
  "externalHeaders": {
    "Authorization": "Bearer {{env.STRIPE_SECRET_KEY}}"
  },
  "externalBody": {
    "charge": "{{charge_id}}",
    "amount": "{{refund_amount}}"
  },
  "callbackKey": "stripe-{{order_id}}",
  "callbackTimeout": "30m",
  "callbackResponseMap": {
    "statusPath": "type",
    "dataPath": "data.object"
  }
}
```
Stripe webhook 進來時 body 是 `{"type":"charge.refunded","data":{"object":{...}}}`，
ResponseMapping 會提取 `type` 作為 status、`data.object` 作為 data。不需要外建 wrapper。

**XML — 製造業 MES：**
```json
{
  "id": "create-quality-ticket",
  "type": "external",
  "externalUrl": "https://mes.factory.com/api/quality/tickets",
  "externalContentType": "application/xml",
  "externalRawBody": "<quality><product_id>{{product_id}}</product_id><defects>{{steps.vision.output}}</defects></quality>",
  "callbackKey": "mes-ticket-{{product_id}}",
  "callbackContentType": "application/xml",
  "callbackTimeout": "5m"
}
```

**Streaming callback — 醫療檢驗（部分結果）：**
```json
{
  "id": "send-lab-order",
  "type": "external",
  "externalUrl": "https://lis.hospital.com/api/orders",
  "externalBody": {
    "patient_id": "{{patient_id}}",
    "test_type": "blood_work"
  },
  "callbackKey": "lab-{{patient_id}}",
  "callbackTimeout": "7d",
  "callbackMode": "streaming",
  "callbackResponseMap": {
    "statusPath": "status",
    "dataPath": "results",
    "donePath": "status",
    "doneValue": "final"
  }
}
```
LIS 第一次 callback：`{status: "partial", results: [{WBC: 5.2}]}`→ 累積，繼續等。
LIS 第二次 callback：`{status: "final", results: [{WBC: 5.2, RBC: 4.5, ...}]}`→ status 欄位值 == doneValue，step 完成。

**長時間人工審批 — 法律合約：**
```json
{
  "id": "lawyer-review",
  "type": "external",
  "externalUrl": "https://lawyer-portal.com/api/review",
  "externalBody": {
    "contract_id": "{{document_id}}",
    "analysis": "{{steps.ai-analysis.output}}"
  },
  "callbackKey": "lawyer-{{document_id}}",
  "callbackTimeout": "14d"
}
```

---

## 執行流程

```
executeDAG()
  │
  ├── step type != "external" → 照舊
  │
  └── step type == "external"
        │
        ├── 1. 解析 template vars（WithFields，支援子欄位存取）
        │
        ├── 2. 查 DB 判斷狀態
        │     ├── 無 record → 首次執行
        │     ├── status=timeout/error → retry（重設 record）
        │     ├── post_sent=false → crash 在 POST 前，需重送
        │     └── post_sent=true → resume，跳過 POST
        │
        ├── 3. 註冊 callback channel（先建接收端，防止快速 callback 競爭）
        │     ├── 碰撞檢查 + 1000 上限
        │     └── channel 建好後，callback 隨時可送達
        │
        ├── 4. 寫 DB（首次 INSERT / retry UPDATE reset）
        │
        ├── 5. 發 HTTP POST（除非 resume 跳過）
        │     ├── 構建 body（JSON / XML / form-encoded）
        │     ├── 硬限 30s，指數退避重試 3 次
        │     └── 成功 → markPostSent
        │
        ├── 6. 暫停等待（goroutine block，其他路徑繼續跑）
        │
        └── 依 callbackMode：
              ├── single：callback → ResponseMapping → output → "success"
              └── streaming：
                    ├── DonePath == DoneValue → 完成
                    │     ├── accumulate=false → 最終 data
                    │     └── accumulate=true → JSON array
                    └── DonePath != DoneValue → 累積，繼續等
```

**關鍵順序**：Register channel（步驟 3）在 HTTP POST（步驟 5）之前。
即使外部服務在 POST 回應前就送 callback，channel 已建好，走 Path A 直接送達。
不會觸發 Path B 的 resumeWorkflow，避免重複 DAG。

### 核心設計

```go
// DAG coordinator — 等所有 step 結束再判定 workflow status
for completed < total {
    select {
    case stepID := <-readyCh:
        go executeStep(stepID)
    case msg := <-doneCh:
        completed++
    }
}
```

### runExternalStep — 最終版（R1-R7 整合）

```go
func (e *workflowExecutor) runExternalStep(ctx context.Context, step *WorkflowStep) (*StepRunResult, error) {
    // 1. 解析 template
    url := e.resolveTemplateWithFields(step.ExternalURL)
    headers := e.resolveTemplateMapWithFields(step.ExternalHeaders)
    key := e.resolveTemplateWithFields(step.CallbackKey)

    mode := step.CallbackMode
    if mode == "" {
        mode = "single"
    }

    // 2. 查 DB 判斷狀態
    pendingCB := queryPendingCallbackByKey(e.cfg.HistoryDB, key)
    var skipPost bool
    switch {
    case pendingCB == nil:
        // 首次執行
    case pendingCB.Status == "timeout" || pendingCB.Status == "error":
        // retry — 重設舊 record
        resetCallbackRecord(e.cfg.HistoryDB, key) // UPDATE status='waiting', post_sent=0
    case pendingCB.PostSent:
        // resume — POST 已成功，跳過
        skipPost = true
    default:
        // post_sent=false — POST 前 crash，需重送
    }

    // 3. 先 Register channel（防止快速 callback 競爭）
    ch := e.callbackMgr.Register(key, ctx, mode)
    if ch == nil {
        return &StepRunResult{Status: "error", Error: "callback key collision or capacity exceeded"}, nil
    }
    defer e.callbackMgr.Unregister(key)

    // 4. 寫 DB（首次 INSERT，retry 已在步驟 2 reset）
    if pendingCB == nil {
        e.recordPendingCallback(key, e.run.ID, step.ID, mode, step.CallbackAuth)
    }

    // 5. 發 HTTP POST
    if !skipPost {
        contentType := step.ExternalContentType
        if contentType == "" {
            contentType = "application/json"
        }
        var bodyBytes []byte
        switch {
        case step.ExternalRawBody != "":
            if strings.Contains(contentType, "xml") {
                bodyBytes = []byte(e.resolveTemplateXMLEscaped(step.ExternalRawBody))
            } else {
                bodyBytes = []byte(e.resolveTemplateWithFields(step.ExternalRawBody))
            }
        default:
            body := e.resolveTemplateMapWithFields(step.ExternalBody)
            bodyBytes, _ = json.Marshal(body)
        }
        if len(bodyBytes) > 1<<20 {
            return &StepRunResult{Status: "error", Error: "request body exceeds 1MB"}, nil
        }

        if _, err := httpPostWithRetry(url, contentType, headers, bodyBytes, 3); err != nil {
            return &StepRunResult{Status: "error", Error: err.Error()}, nil
        }
        e.markPostSent(key)
    } else {
        logInfo("resume: skipping HTTP POST", "key", key, "step", step.ID)
    }

    // 6. 通知 dashboard + 等待
    e.publishEvent("step_waiting", map[string]any{"stepId": step.ID, "callbackKey": key})

    timeout := parseDuration(step.CallbackTimeout, 1*time.Hour)
    if mode == "single" {
        return e.waitSingleCallback(ctx, ch, key, step, timeout)
    }
    return e.waitStreamingCallback(ctx, ch, key, step, timeout)
}
```

### waitSingleCallback — 整合版

```go
func (e *workflowExecutor) waitSingleCallback(ctx context.Context, ch <-chan CallbackResult, key string, step *WorkflowStep, timeout time.Duration) (*StepRunResult, error) {
    timer := time.NewTimer(timeout)
    defer timer.Stop()

    select {
    case result := <-ch:
        e.clearPendingCallback(key)
        output := e.applyResponseMapping(result.Body, step.CallbackResponseMap)
        return &StepRunResult{Status: "success", Output: output}, nil

    case <-timer.C:
        e.clearPendingCallback(key)
        if step.OnTimeout == "skip" {
            return &StepRunResult{Status: "skipped", Error: "callback timeout"}, nil
        }
        return &StepRunResult{Status: "error", Error: fmt.Sprintf("callback %s timeout after %s", key, step.CallbackTimeout)}, nil

    case <-ctx.Done():
        e.clearPendingCallback(key)
        return &StepRunResult{Status: "cancelled"}, nil
    }
}
```

### waitStreamingCallback — 整合版（含 accumulate 模式）

```go
func (e *workflowExecutor) waitStreamingCallback(ctx context.Context, ch <-chan CallbackResult, key string, step *WorkflowStep, timeout time.Duration) (*StepRunResult, error) {
    timer := time.NewTimer(timeout)
    defer timer.Stop()

    var accumulated []string // 所有 callback 的 mapped output
    var lastOutput string    // 最後一筆 mapped output

    for {
        select {
        case result := <-ch:
            mapped := e.applyResponseMapping(result.Body, step.CallbackResponseMap)
            lastOutput = mapped
            if step.CallbackAccumulate {
                accumulated = append(accumulated, mapped)
            }

            // 檢查是否為最終 callback
            if step.CallbackResponseMap != nil && step.CallbackResponseMap.DonePath != "" {
                if extractJSONPath(result.Body, step.CallbackResponseMap.DonePath) == step.CallbackResponseMap.DoneValue {
                    e.clearPendingCallback(key)
                    output := lastOutput
                    if step.CallbackAccumulate {
                        output = "[" + strings.Join(accumulated, ",") + "]"
                    }
                    return &StepRunResult{Status: "success", Output: output}, nil
                }
            }
            continue

        case <-timer.C:
            e.clearPendingCallback(key)
            output := lastOutput
            if step.CallbackAccumulate && len(accumulated) > 0 {
                output = "[" + strings.Join(accumulated, ",") + "]"
            }
            if output != "" {
                if step.OnTimeout == "skip" {
                    return &StepRunResult{Status: "skipped", Output: output, Error: "streaming timeout (partial)"}, nil
                }
                return &StepRunResult{Status: "error", Output: output, Error: "streaming timeout (partial)"}, nil
            }
            if step.OnTimeout == "skip" {
                return &StepRunResult{Status: "skipped", Error: "callback timeout"}, nil
            }
            return &StepRunResult{Status: "error", Error: "callback timeout"}, nil

        case <-ctx.Done():
            e.clearPendingCallback(key)
            return &StepRunResult{Status: "cancelled"}, nil
        }
    }
}
```

### ResponseMapping + extractJSONPath — 整合版

```go
func (e *workflowExecutor) applyResponseMapping(body string, mapping *ResponseMapping) string {
    if mapping == nil {
        return body
    }
    if body == "" {
        logWarn("callback body is empty, skipping ResponseMapping")
        return body
    }
    if mapping.DataPath != "" {
        if extracted := extractJSONPath(body, mapping.DataPath); extracted != "" {
            return extracted
        }
    }
    return body
}

// extractJSONPath — dot-notation JSON 路徑提取
// 支援："data.object.status"（巢狀）, "items.0.id"（陣列索引）, "status"（頂層）
// 不支援：bracket notation, escaped dots, wildcards
// 路徑格式限制：[a-zA-Z0-9_.] （由 jsonPathRegex 驗證）
func extractJSONPath(jsonStr string, path string) string {
    var data any
    if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
        return ""
    }

    current := data
    for _, part := range strings.Split(path, ".") {
        switch v := current.(type) {
        case map[string]any:
            current = v[part]
        case []any:
            idx, err := strconv.Atoi(part)
            if err != nil || idx < 0 || idx >= len(v) {
                return ""
            }
            current = v[idx]
        default:
            return ""
        }
        if current == nil {
            return ""
        }
    }

    switch v := current.(type) {
    case string:
        return v
    case bool:
        if v { return "true" }
        return "false"
    case float64:
        return strconv.FormatFloat(v, 'f', -1, 64)
    default:
        b, _ := json.Marshal(v)
        return string(b)
    }
}
```

### 結構化欄位存取 — `{{steps.id.output.field}}`

Phase 1 隔離策略：獨立函數，不動核心 `resolveTemplate()`。
Phase 2 穩定後整合進核心 template engine。

```go
// workflow_external.go — 獨立檔案

func resolveStepOutputField(output string, fieldPath string) string {
    if fieldPath == "" {
        return output
    }
    return extractJSONPath(output, fieldPath)
}

func (e *workflowExecutor) resolveTemplateMapWithFields(m map[string]string) map[string]string {
    result := make(map[string]string, len(m))
    for k, v := range m {
        result[k] = e.resolveTemplateWithFields(v)
    }
    return result
}

func (e *workflowExecutor) resolveTemplateWithFields(tmpl string) string {
    result := e.resolveTemplate(tmpl) // 先處理標準 vars

    // 再處理 {{steps.id.output.field}} 語法
    re := regexp.MustCompile(`\{\{steps\.(\w+)\.output\.([a-zA-Z0-9_.]+)\}\}`)
    result = re.ReplaceAllStringFunc(result, func(match string) string {
        parts := re.FindStringSubmatch(match)
        stepID, fieldPath := parts[1], parts[2]
        if sr, ok := e.wCtx.Steps[stepID]; ok {
            return resolveStepOutputField(sr.Output, fieldPath)
        }
        return match
    })
    return result
}

func (e *workflowExecutor) resolveTemplateXMLEscaped(tmpl string) string {
    re := regexp.MustCompile(`\{\{([^}]+)\}\}`)
    return re.ReplaceAllStringFunc(tmpl, func(match string) string {
        value := e.resolveTemplate(match)
        if value == match {
            return match
        }
        value = strings.ReplaceAll(value, "&", "&amp;")
        value = strings.ReplaceAll(value, "<", "&lt;")
        value = strings.ReplaceAll(value, ">", "&gt;")
        value = strings.ReplaceAll(value, "\"", "&quot;")
        value = strings.ReplaceAll(value, "'", "&apos;")
        return value
    })
}
```

範例：
```yaml
# OCR 回傳 {"extracted_text": "發票...", "confidence": 0.95}
- id: validate
  prompt: "校正以下文字：{{steps.send-ocr.output.extracted_text}}"
  # → 解析為 "校正以下文字：發票..."

# Stripe 回傳 {"id": "re_xxx", "amount": 1234, "status": "succeeded"}
- id: check-refund
  type: condition
  if: "{{steps.stripe-refund.output.status}} == succeeded"
  then: notify-customer
  else: escalate
```

---

## Callback 機制

### 新 API Endpoint

```
POST /api/callbacks/{key}
Content-Type: application/json（或 application/xml，依 callbackContentType）
Authorization: Bearer <dashboard-token>    ← 必要（Phase 1 用現有 token）

# JSON callback（預設）
{
  "status": "success",
  "data": { ... 任意 JSON ... }
}

# 或任意 webhook body（使用 ResponseMapping 提取）
{
  "type": "charge.refunded",
  "data": { "object": { "id": "re_xxx", "amount": 1234 } }
}

# 或 XML callback
<response><ticket_id>T123</ticket_id><status>success</status></response>
```

回應格式（統一 JSON）：
- `200 {"status": "delivered"}` — 路徑 A：goroutine 還活著，直接送達
- `200 {"status": "accumulated"}` — streaming 模式，部分結果已累積
- `200 {"status": "resumed"}` — 路徑 B：daemon 重啟過，從 DB 恢復
- `400 {"error": "missing callback key"}` — key 為空
- `404 {"error": "no pending callback"}` — 找不到等待中的 callback
- `409 {"error": "callback already delivered"}` — single 模式重複送達

### Callback Manager — 整合版

```go
// callback.go

type CallbackManager struct {
    mu       sync.RWMutex
    channels map[string]*callbackEntry
    dbPath   string
}

type callbackEntry struct {
    ch   chan CallbackResult
    mode string // "single" or "streaming"
}

type CallbackResult struct {
    Status      string    `json:"status"`
    Body        string    `json:"body"`
    ContentType string    `json:"contentType"`
    RecvAt      time.Time
}

func (cm *CallbackManager) Register(key string, ctx context.Context, mode string) <-chan CallbackResult {
    cm.mu.Lock()
    defer cm.mu.Unlock()

    // 碰撞檢查 — 防止靜態 callbackKey 的並發 run 覆蓋 channel
    if _, exists := cm.channels[key]; exists {
        logWarn("callback key collision", "key", key)
        return nil
    }
    if len(cm.channels) >= 1000 {
        logWarn("callback manager at capacity", "count", len(cm.channels))
        return nil
    }

    bufSize := 1
    if mode == "streaming" {
        bufSize = 256
    }

    ch := make(chan CallbackResult, bufSize)
    cm.channels[key] = &callbackEntry{ch: ch, mode: mode}

    // Context cleanup — daemon shutdown 或 workflow cancel 時自動清理
    go func() {
        <-ctx.Done()
        cm.Unregister(key)
    }()

    return ch
}

func (cm *CallbackManager) Deliver(key string, result CallbackResult) (string, error) {
    cm.mu.RLock()
    entry, ok := cm.channels[key]
    cm.mu.RUnlock()

    if !ok {
        return "", fmt.Errorf("no waiting callback for key: %s", key)
    }

    if entry.mode == "single" {
        if isCallbackDelivered(cm.dbPath, key, 0) {
            return "", fmt.Errorf("callback already delivered")
        }
        markCallbackDelivered(cm.dbPath, key, 0, result)
        entry.ch <- result
        return "delivered", nil
    }

    // Streaming — 允許多次 callback，每次寫 DB + 送 channel
    appendStreamingCallback(cm.dbPath, key, result)
    select {
    case entry.ch <- result:
        return "accumulated", nil
    case <-time.After(5 * time.Second):
        logWarn("streaming buffer full, stored in DB only", "key", key)
        return "accumulated", nil
    }
}

func (cm *CallbackManager) Unregister(key string) {
    cm.mu.Lock()
    defer cm.mu.Unlock()
    if entry, ok := cm.channels[key]; ok {
        close(entry.ch)
        delete(cm.channels, key)
    }
    // 已不在 map → safe no-op（防止 double close panic）
}

func (cm *CallbackManager) HasChannel(key string) bool {
    cm.mu.RLock()
    defer cm.mu.RUnlock()
    _, ok := cm.channels[key]
    return ok
}

func (cm *CallbackManager) GetMode(key string) string {
    cm.mu.RLock()
    defer cm.mu.RUnlock()
    if entry, ok := cm.channels[key]; ok {
        return entry.mode
    }
    return ""
}

// ReplayAccumulated — daemon 重啟後，把 DB 裡已累積的 streaming callbacks 送進 channel
// 呼叫時機：Register() 之後、resumeDAG() 之前
func (cm *CallbackManager) ReplayAccumulated(key string, results []CallbackResult) {
    cm.mu.RLock()
    entry, ok := cm.channels[key]
    cm.mu.RUnlock()

    if !ok || entry.mode != "streaming" {
        return
    }
    for _, r := range results {
        select {
        case entry.ch <- r:
        default:
            logWarn("replay: buffer full, skipping", "key", key)
        }
    }
}
```

### HTTP Callback Handler — 整合版

```go
// http_workflow.go

mux.HandleFunc("/api/callbacks/", func(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")

    if r.Method != http.MethodPost {
        w.WriteHeader(http.StatusMethodNotAllowed)
        json.NewEncoder(w).Encode(map[string]string{"error": "method not allowed"})
        return
    }

    key := strings.TrimPrefix(r.URL.Path, "/api/callbacks/")
    if key == "" {
        w.WriteHeader(http.StatusBadRequest)
        json.NewEncoder(w).Encode(map[string]string{"error": "missing callback key"})
        return
    }

    // 認證 — 先查 DB 取得該 callback 的 auth 模式
    cbRecord, _ := queryPendingCallbackByKey(cfg.HistoryDB, key)
    authMode := "bearer" // 預設
    if cbRecord != nil {
        authMode = cbRecord.AuthMode
    }

    switch authMode {
    case "open":
        // 不驗 token — 適用第三方 webhook（Stripe, GitHub）
        // 安全靠 callbackKey 的不可預測性（含 {{runId}}）
    case "bearer", "":
        // 用現有 dashboard token 驗證
        if !validateBearerToken(r) {
            w.WriteHeader(http.StatusUnauthorized)
            json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
            return
        }
    // case "signature": Phase 3 HMAC
    }

    // 判斷 mode（先查 channel，沒有再查 DB）
    mode := callbackMgr.GetMode(key)
    if mode == "" {
        if cbRecord != nil && cbRecord.Status == "waiting" {
            mode = cbRecord.Mode
        }
    }

    // Single 模式冪等檢查
    if mode != "streaming" && isCallbackDelivered(cfg.HistoryDB, key, 0) {
        w.WriteHeader(http.StatusConflict)
        json.NewEncoder(w).Encode(map[string]string{"error": "callback already delivered"})
        return
    }

    // 讀取 body（1MB 上限）
    bodyBytes, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
    if err != nil {
        w.WriteHeader(http.StatusBadRequest)
        json.NewEncoder(w).Encode(map[string]string{"error": "body too large (max 1MB)"})
        return
    }

    contentType := r.Header.Get("Content-Type")
    result := CallbackResult{
        Body:        string(bodyBytes),
        ContentType: contentType,
        RecvAt:      time.Now(),
    }
    // 嘗試從 JSON body 提取 status
    if strings.Contains(contentType, "json") || contentType == "" {
        var parsed map[string]any
        if json.Unmarshal(bodyBytes, &parsed) == nil {
            if s, ok := parsed["status"].(string); ok {
                result.Status = s
            }
        }
    }

    // 路徑 A：goroutine 還活著 → 直接送達
    if callbackMgr.HasChannel(key) {
        status, err := callbackMgr.Deliver(key, result)
        if err != nil {
            if strings.Contains(err.Error(), "already delivered") {
                w.WriteHeader(http.StatusConflict)
                json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
                return
            }
        } else {
            auditLog(cfg.HistoryDB, "callback."+status, "http",
                fmt.Sprintf("key=%s", key), clientIP(r))
            json.NewEncoder(w).Encode(map[string]string{"status": status})
            return
        }
    }

    // 路徑 B：goroutine 已死 → 從 DB 恢復
    // queryPendingCallback 只查 status='waiting'，防止 webhook 重試觸發重複 resume
    cb, err := queryPendingCallback(cfg.HistoryDB, key)
    if err != nil {
        w.WriteHeader(http.StatusNotFound)
        json.NewEncoder(w).Encode(map[string]string{"error": "no pending callback"})
        return
    }

    // 先標記 delivered 再 resume — 確保重試時找不到
    markCallbackDelivered(cfg.HistoryDB, key, 0, result)

    run, err := queryWorkflowRunByID(cfg.HistoryDB, cb.WorkflowRunID)
    if err != nil {
        w.WriteHeader(http.StatusInternalServerError)
        json.NewEncoder(w).Encode(map[string]string{"error": "cannot load workflow run"})
        return
    }

    // 套用 ResponseMapping
    output := result.Body
    wf, err := loadWorkflowByName(cfg, run.WorkflowName)
    if err == nil {
        for _, step := range wf.Steps {
            if step.ID == cb.StepID && step.CallbackResponseMap != nil {
                output = applyResponseMapping(result.Body, step.CallbackResponseMap)
            }
        }
    }

    run.StepResults[cb.StepID].Status = "success"
    run.StepResults[cb.StepID].Output = output
    run.StepResults[cb.StepID].FinishedAt = time.Now().Format(time.RFC3339)
    recordWorkflowRun(cfg.HistoryDB, run)

    if wf != nil {
        wCtx := rebuildContextFromRun(wf, run)
        go resumeWorkflow(context.Background(), cfg, wf, run, wCtx, state, sem, childSem)
    }

    auditLog(cfg.HistoryDB, "callback.resumed", "http",
        fmt.Sprintf("key=%s run=%s", key, run.ID), clientIP(r))
    json.NewEncoder(w).Encode(map[string]string{"status": "resumed"})
})
```

### Cancel Endpoint

```go
// Server 維護 cancel functions
type Server struct {
    // ... 既有欄位 ...
    runCancellers sync.Map // map[runID]context.CancelFunc
}

// POST /workflow-runs/{id}/cancel
// cancel handler（加入既有的 /workflow-runs/ handler）:
//   runID := extractRunID(r)
//   if cancel, ok := s.runCancellers.Load(runID); ok {
//       cancel.(context.CancelFunc)()
//       s.runCancellers.Delete(runID)
//   }
//   markRunCancelled(cfg.HistoryDB, runID)

// executeWorkflow 開始時：
//   ctx, cancel := context.WithCancel(ctx)
//   s.runCancellers.Store(run.ID, cancel)
//   defer s.runCancellers.Delete(run.ID)
```

---

## DB Schema — 整合版

```sql
CREATE TABLE IF NOT EXISTS workflow_callbacks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    callback_key TEXT NOT NULL,
    workflow_run_id TEXT NOT NULL,
    step_id TEXT NOT NULL,
    mode TEXT NOT NULL DEFAULT 'single',      -- single / streaming
    status TEXT NOT NULL DEFAULT 'waiting',    -- waiting / delivered / timeout / cancelled
    sequence INTEGER NOT NULL DEFAULT 0,      -- streaming 第幾次（single 永遠 = 0）
    post_sent INTEGER NOT NULL DEFAULT 0,     -- HTTP POST 是否已成功發送（0/1）
    auth_mode TEXT NOT NULL DEFAULT 'bearer', -- bearer / open / signature(Phase 3)
    request_url TEXT DEFAULT '',
    request_body TEXT DEFAULT '',
    response_body TEXT DEFAULT '',
    content_type TEXT DEFAULT 'application/json',
    created_at TEXT NOT NULL,
    resolved_at TEXT DEFAULT '',
    timeout_at TEXT DEFAULT '',                -- 預計超時時間
    FOREIGN KEY (workflow_run_id) REFERENCES workflow_runs(id)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_callbacks_key_seq ON workflow_callbacks(callback_key, sequence);
CREATE INDEX IF NOT EXISTS idx_callbacks_run ON workflow_callbacks(workflow_run_id);
CREATE INDEX IF NOT EXISTS idx_callbacks_status ON workflow_callbacks(status);
```

### DB Helper Functions

```go
// 查詢：只回傳 status='waiting' 的記錄
func queryPendingCallback(dbPath, key string) (*CallbackRecord, error)      // WHERE callback_key=? AND status='waiting' AND sequence=0
func queryPendingCallbackByKey(dbPath, key string) (*CallbackRecord, error) // WHERE callback_key=? AND sequence=0 (不限 status)
func queryPendingCallbacksByRun(dbPath, runID string) ([]CallbackRecord, error)

// 寫入
func recordPendingCallback(dbPath, key, runID, stepID, mode, authMode string) // INSERT, post_sent=0, auth_mode
func resetCallbackRecord(dbPath, key string)                                // UPDATE status='waiting', post_sent=0（retry 用）
func markPostSent(dbPath, key string)                                       // UPDATE post_sent=1
func markCallbackDelivered(dbPath, key string, seq int, result CallbackResult)
func appendStreamingCallback(dbPath, key string, result CallbackResult)     // INSERT with next sequence

// 檢查
func isCallbackDelivered(dbPath, key string, seq int) bool                  // status='delivered'
func checkExpiredCallbacks(dbPath, runID string) []CallbackRecord            // timeout_at < now
func queryStreamingCallbacks(dbPath, key string) []CallbackResult           // 所有已收到的 streaming records
```

Key: `(callback_key, sequence)` 複合唯一。Single 模式 sequence=0，Streaming 遞增 0,1,2...

---

## Checkpoint + Resume（斷電恢復）— 整合版

### 設計原則

每步完成後 checkpoint 到 DB。Crash 後從斷點恢復，不需要完整 workflow 序列化。

### Checkpoint 機制

```go
// executeDAG() 的 doneCh handler
case msg := <-doneCh:
    // ... StepResults 更新 + WorkflowContext 更新 ...

    // Checkpoint：workflow status 只在這裡設定（不在 runExternalStep 裡改）
    if hasWaitingExternalStep(e.run.StepResults) {
        e.run.Status = "waiting"
    }
    e.checkpointRun()

func (e *workflowExecutor) checkpointRun() {
    if err := recordWorkflowRun(e.cfg.HistoryDB, e.run); err != nil {
        logError("checkpoint failed", "run", e.run.ID, "error", err)
    }
}

func hasWaitingExternalStep(results map[string]*StepRunResult) bool {
    for _, sr := range results {
        if sr.Status == "waiting" { return true }
    }
    return false
}

// Workflow 定義 hash — resume 時比對是否被修改
func hashWorkflow(wf *Workflow) string {
    b, _ := json.Marshal(wf.Steps)
    return fmt.Sprintf("%x", sha256.Sum256(b))
}
```

**executeWorkflow() 開始時**：`e.run.WorkflowHash = hashWorkflow(wf)`
**resumeWorkflow() 開始時**：比對 hash → 不匹配則 log warning

**成本**：每步一次 sqlite3 寫入，10 步以內可忽略。50+ 步改為每 5 步或每 3 秒一次。
**原子性**：workflow status 只由 DAG coordinator 設定，避免 goroutine race。

### Workflow 狀態擴充

```
running   → 正常執行中
waiting   → 有 external step 在等 callback（新增）
success   → 全部完成
error     → 有 step 失敗
timeout   → 整體超時
cancelled → 被取消
```

### Resume 機制 — 整合版

Daemon 啟動時掃描未完成的 workflow：

```go
func (s *Server) recoverPendingWorkflows() {
    runs := queryDB(cfg.HistoryDB,
        "SELECT * FROM workflow_runs WHERE status IN ('waiting','running')")

    for _, run := range runs {
        wf, err := loadWorkflowByName(cfg, run.WorkflowName)
        if err != nil {
            logWarn("cannot recover workflow", "run", run.ID, "name", run.WorkflowName)
            markRunError(cfg.HistoryDB, run.ID, "workflow definition not found on recovery")
            continue
        }

        // Workflow 定義版本比對
        if run.WorkflowHash != "" && run.WorkflowHash != hashWorkflow(wf) {
            logWarn("workflow definition changed since run started", "run", run.ID)
        }

        // 處理已超時的 callback
        for _, cb := range checkExpiredCallbacks(cfg.HistoryDB, run.ID) {
            markCallbackTimeout(cfg.HistoryDB, cb.CallbackKey)
            run.StepResults[cb.StepID].Status = "timeout"
            run.StepResults[cb.StepID].Error = "callback timeout (daemon restarted)"
            run.StepResults[cb.StepID].FinishedAt = time.Now().Format(time.RFC3339)
        }

        // 未超時的 streaming callback → 重播已累積的結果
        for _, cb := range queryPendingCallbacksByRun(cfg.HistoryDB, run.ID) {
            if cb.Mode == "streaming" {
                accumulated := queryStreamingCallbacks(cfg.HistoryDB, cb.CallbackKey)
                callbackMgr.ReplayAccumulated(cb.CallbackKey, accumulated)
            }
        }

        wCtx := rebuildContextFromRun(wf, run)
        go resumeWorkflow(context.Background(), cfg, wf, run, wCtx, state, sem, childSem)
    }
}

func rebuildContextFromRun(wf *Workflow, run *WorkflowRun) *WorkflowContext {
    wCtx := &WorkflowContext{
        Input: run.Variables,
        Steps: make(map[string]*WorkflowStepResult),
        Env:   envSnapshot(),
    }
    for id, sr := range run.StepResults {
        if sr.Status == "success" || sr.Status == "skipped" {
            wCtx.Steps[id] = &WorkflowStepResult{
                Output: sr.Output, Status: sr.Status, Error: sr.Error,
            }
        }
    }
    return wCtx
}
```

### resumeDAG()：從斷點恢復

複用 `executeDAG()`，加 `resumeFrom` 參數。
`runExternalStep()` 用 DB 的 `post_sent` flag 判斷是否重送 POST（不用 isResuming flag）。

```go
func (e *workflowExecutor) executeDAG(ctx context.Context, resumeFrom map[string]*StepRunResult) error {
    // ... 建立 remaining, dependents, readyCh, doneCh ...

    if resumeFrom != nil {
        // 標記已完成的 step
        for id, sr := range resumeFrom {
            if sr.Status == "success" || sr.Status == "skipped" || sr.Status == "error" || sr.Status == "timeout" {
                completed++
                for _, dep := range dependents[id] {
                    remaining[dep]--
                }
            }
        }

        // 重播 condition 分支：跳過 unchosen branch
        for _, step := range e.workflow.Steps {
            if step.Type != "condition" { continue }
            sr, ok := resumeFrom[step.ID]
            if !ok || sr.Status != "success" { continue }
            for _, skippedID := range e.replayConditionResult(&step, sr, remaining, dependents) {
                completed++
                for _, dep := range dependents[skippedID] {
                    remaining[dep]--
                }
            }
        }

        // Seed 可以跑的 step（跳過已完成的）
        for id, cnt := range remaining {
            if cnt == 0 {
                if sr, ok := resumeFrom[id]; ok && (sr.Status == "success" || sr.Status == "skipped" || sr.Status == "error" || sr.Status == "timeout") {
                    continue
                }
                readyCh <- id
            }
        }
    } else {
        for id, cnt := range remaining {
            if cnt == 0 { readyCh <- id }
        }
    }
    // ... main loop 不變 ...
}
```

```go
func (e *workflowExecutor) replayConditionResult(
    step *WorkflowStep, sr *StepRunResult,
    remaining map[string]int, dependents map[string][]string,
) []string {
    chosenTarget := sr.Output

    var skipTarget string
    if chosenTarget == step.Then && step.Else != "" {
        skipTarget = step.Else
    } else if chosenTarget == step.Else && step.Then != "" {
        skipTarget = step.Then
    }

    if skipTarget == "" {
        return nil
    }

    e.run.StepResults[skipTarget] = &StepRunResult{
        StepID: skipTarget,
        Status: "skipped",
    }
    return []string{skipTarget}
}
```

### Callback 的兩條路徑

```
Callback 進來（POST /api/callbacks/{key}）
    │
    ├── Single 模式冪等檢查：DB 已 delivered → 409 Conflict
    │
    ├── 路徑 A：goroutine 還活著（CallbackManager 有 channel）
    │   ├── single → 寫 DB delivered → channel 送達 → DAG 繼續
    │   └── streaming → 寫 DB accumulated → channel 送達 → 等判斷 done
    │
    └── 路徑 B：goroutine 已死（daemon 重啟過）
        ├── 查 DB：workflow_callbacks 找到 waiting 記錄
        ├── 套用 ResponseMapping
        ├── 更新 step output → recordWorkflowRun()
        └── resumeDAG() 從斷點繼續
```

### 邊界情況處理

| 情況 | 處理 |
|------|------|
| Daemon 重啟，callback 還沒來 | `recoverPendingWorkflows()` 檢查 timeout_at，未超時 → resumeDAG（external step 會重新 block 等 callback）|
| Daemon 重啟，callback 在掛掉期間來了 | 外部服務應自行 retry。Tetora 啟動後超時的會標記 timeout |
| Single callback 來了兩次 | 冪等檢查：先查 DB status，已 delivered → 回 409 Conflict |
| Streaming callback 來了多次 | 每次都送進 channel，sequence 遞增，直到 DonePath == DoneValue |
| Workflow 定義被刪了 | Recovery 時找不到定義 → 標記 run error + log warn |
| Callback timeout + callback 同時到 | Buffered channel + select → 先到的贏。Deliver 前先寫 DB |
| CallbackManager 滿了（1000 上限）| Register 回傳 nil → step error |
| Condition step 後面接 external | Resume 時先 replayConditionResult() 跳過 unchosen branch |
| Parallel step 內含 external | External 會 block 整個 parallel step 直到 callback |
| XML callback | HTTP handler 讀 raw body bytes，不強制 JSON 解析 |
| Streaming timeout 但有部分結果 | 用最後收到的 data 作為 output，status 依 onTimeout 決定 |
| 使用者取消等待中 workflow | `POST /workflow-runs/{id}/cancel` → cancel context → 所有 waiting step 收到 ctx.Done() → "cancelled" |
| 靜態 callbackKey 碰撞 | Register() 碰撞檢查 → step error。Validation 建議 callbackKey 包含 `{{runId}}` |
| Webhook 重試（Path B）| `queryPendingCallback` 只查 `status='waiting'`，已 delivered → 404 |
| Resume 重送 HTTP POST | `post_sent` flag 判斷：已送 → 跳過 POST，未送 → 重送 |
| Workflow 定義在等待期間被修改 | `recordWorkflowRun` 記 `workflowHash`，resume 時比對 → 不匹配則 log warning |
| Parallel branch 失敗 + external 等待中 | Coordinator 等所有 step 結束（含 external timeout/callback）再判定最終 status。不提前標記 error |
| Checkpoint DB 寫入失敗 | `checkpointRun()` log error 繼續執行。不中斷 workflow，但標記 attention |
| Graceful shutdown + 飛行中 callback | `srv.Shutdown()` 5s drain period → 讓 in-flight callback 有機會送達 → 再 cancel contexts |
| Streaming callback DB 膨脹 | Phase 3 加 TTL 清理：已完成 run 的中間 streaming records 7 天後清除 |
| POST 成功但 DB 未記錄就 crash | 先寫 DB（post_sent=false）再 POST → 更新 post_sent=true。Resume 檢查 flag 決定是否重送 |
| 第三方 webhook 無法帶 Bearer token | `callbackAuth: "open"` 跳過 token 驗證，安全性靠 callbackKey 含 `{{runId}}` 不可預測 |
| retryMax + external step | retry 時 `resetCallbackRecord()` 重設 status/post_sent → 不會撞 unique constraint |
| 快速 callback（POST 回應前就來）| Register channel 在 POST 之前（步驟 3 < 步驟 5），channel 已建好 → 走 Path A，不觸發重複 DAG |
| Callback body 空或格式錯誤 | `applyResponseMapping` 對空/無效 JSON 回傳原始 body + log warning，不 crash |

---

## Dashboard UI 呈現

### Workflow Editor

新增 `external` step type 到 Add Step 下拉：

```
[+ Add Step ▾]
  dispatch
  skill
  condition
  parallel
  tool_call
  delay
  notify
  external    ← 新增
```

Property Panel 欄位：

```
┌─ external step ──────────────────┐
│ ID: [send-ocr                  ] │
│ Type: external                    │
│                                   │
│ URL: [https://ocr.com/api      ] │
│ Content-Type: [application/json▾] │
│ Headers:                          │
│   [Authorization] [Bearer..    ] │
│   [+ Add Header]                 │
│ Body (KV):                        │
│   [image_url] [{{image_url}}   ] │
│   [+ Add Field]                  │
│ — 或 Raw Body —                   │
│   [<xml>...</xml>              ] │
│                                   │
│ Callback Key: [ocr-{{runId}}   ] │
│ Mode: [single ▾] (single/stream) │
│ Timeout: [5m                   ] │
│ On Timeout: [stop ▾]             │
│                                   │
│ Response Mapping (選填):          │
│   Status Path: [type           ] │
│   Data Path:   [data.object    ] │
│                                   │
│ Depends On: ☑ classify            │
└───────────────────────────────────┘
```

### Workflow Runs 面板

```
✅ classify (2s)
⏳ send-ocr — waiting for callback (elapsed: 1m23s, timeout: 5m)
   └── streaming: received 2/? callbacks
⬚ validate (pending)
```

### SSE Events

- `step_waiting` — step 進入等待狀態（含 callbackKey, elapsed）
- `step_callback_received` — streaming 模式收到部分結果
- `workflow_status_changed` — workflow status 變更

---

## Validation 規則 — 整合版

```go
var jsonPathRegex = regexp.MustCompile(`^[a-zA-Z0-9_.]+$`)

case "external":
    // URL
    if step.ExternalURL == "" {
        errs = append(errs, "external step must have externalUrl")
    }
    if step.ExternalURL != "" && !strings.HasPrefix(step.ExternalURL, "https://") && !strings.HasPrefix(step.ExternalURL, "{{") {
        errs = append(errs, "externalUrl must use HTTPS")
    }

    // Body 互斥
    if len(step.ExternalBody) > 0 && step.ExternalRawBody != "" {
        errs = append(errs, "externalBody and externalRawBody are mutually exclusive")
    }

    // Content-Type 白名單
    if step.ExternalContentType != "" {
        allowed := []string{"application/json", "application/xml", "text/xml",
            "application/x-www-form-urlencoded", "text/plain"}
        if !contains(allowed, step.ExternalContentType) {
            errs = append(errs, "unsupported externalContentType")
        }
    }

    // Callback key
    if step.CallbackKey == "" {
        errs = append(errs, "external step must have callbackKey")
    }
    if step.CallbackKey != "" && !isValidCallbackKey(step.CallbackKey) {
        errs = append(errs, "callbackKey must match [a-zA-Z0-9._-] (template vars allowed)")
    }
    if step.CallbackKey != "" && !strings.Contains(step.CallbackKey, "{{runId}}") {
        logWarn("callbackKey without {{runId}} may collide on concurrent runs", "step", step.ID)
    }

    // Timeout
    if step.CallbackTimeout != "" {
        dur, err := parseDurationSafe(step.CallbackTimeout)
        if err != nil {
            errs = append(errs, "invalid callbackTimeout format")
        } else if dur > 30*24*time.Hour {
            errs = append(errs, "callbackTimeout must not exceed 30d")
        } else if dur > 7*24*time.Hour {
            logWarn("long callback timeout", "step", step.ID, "timeout", step.CallbackTimeout)
        }
    }

    // Mode + OnTimeout
    if step.CallbackMode != "" && step.CallbackMode != "single" && step.CallbackMode != "streaming" {
        errs = append(errs, "callbackMode must be 'single' or 'streaming'")
    }
    if step.OnTimeout != "" && step.OnTimeout != "stop" && step.OnTimeout != "skip" {
        errs = append(errs, "onTimeout must be 'stop' or 'skip'")
    }

    // Accumulate 只在 streaming 模式有意義
    if step.CallbackAccumulate && step.CallbackMode != "streaming" {
        errs = append(errs, "callbackAccumulate only applies to streaming mode")
    }

    // CallbackAuth
    if step.CallbackAuth != "" && step.CallbackAuth != "bearer" && step.CallbackAuth != "open" && step.CallbackAuth != "signature" {
        errs = append(errs, "callbackAuth must be 'bearer', 'open', or 'signature'")
    }
    if step.CallbackAuth == "signature" {
        errs = append(errs, "callbackAuth 'signature' is not yet supported (Phase 3)")
    }
    if step.CallbackAuth == "open" && !strings.Contains(step.CallbackKey, "{{runId}}") {
        errs = append(errs, "callbackAuth 'open' requires callbackKey to contain {{runId}} for security")
    }

    // ResponseMapping
    if step.CallbackResponseMap != nil {
        // XML callback + ResponseMapping 不可並用（extractJSONPath 只支援 JSON）
        if step.CallbackContentType != "" && !strings.Contains(step.CallbackContentType, "json") {
            errs = append(errs, "callbackResponseMap only works with JSON callbacks")
        }
        // Streaming 必須有 DonePath/DoneValue
        if step.CallbackMode == "streaming" {
            if step.CallbackResponseMap.DonePath == "" {
                errs = append(errs, "streaming mode requires callbackResponseMap.donePath")
            }
            if step.CallbackResponseMap.DoneValue == "" {
                errs = append(errs, "streaming mode requires callbackResponseMap.doneValue")
            }
        }
        // Path 格式驗證
        for _, p := range []string{
            step.CallbackResponseMap.StatusPath,
            step.CallbackResponseMap.DataPath,
            step.CallbackResponseMap.DonePath,
        } {
            if p != "" && !jsonPathRegex.MatchString(p) {
                errs = append(errs, "ResponseMapping path must match [a-zA-Z0-9_.] (got: "+p+")")
            }
        }
    }
```

---

## 安全考量

### Callback 認證

**Phase 1：用現有 Bearer Token（必要）**
- Callback endpoint 走 dashboard 的 token 認證
- 外部服務需要知道 token → 適合自有服務

**Phase 3：Per-callback HMAC Secret**
- `callbackSecret = hex(sha256(callbackKey + serverSecret))`
- 發 request 時帶 `callbackUrl` 和 `callbackSecret`
- Callback 驗證 `X-Callback-Secret` header
- 第三方不需要 dashboard token

### Request 限制

- `externalUrl` 只允許 HTTPS（validation 擋）
- HTTP request timeout 硬限 30 秒
- Request body 大小限制 1MB
- Callback body 大小限制 1MB
- 初始 request 重試：指數退避 3 次（1s, 2s, 4s）

### Audit Trail

所有 callback 操作寫 audit log：
- `callback.delivered` — 正常送達
- `callback.accumulated` — streaming 部分結果
- `callback.resumed` — daemon 重啟後恢復
- `callback.timeout` — 超時
- `callback.conflict` — 重複送達被拒

---

## 實作順序

### Phase 1：核心機制 + Single Callback + Checkpoint
1. `WorkflowStep` 加 external 欄位（workflow.go）
2. `ResponseMapping` struct（workflow.go）
3. `extractJSONPath()` + `applyResponseMapping()`（workflow.go）
4. `resolveStepOutputField()` — `{{steps.id.output.field}}` 點號語法（workflow.go）
5. `CallbackManager` 元件 — single 模式（callback.go）
6. `checkpointRun()` — DAG coordinator doneCh 後寫 DB（workflow_exec.go）
7. `runExternalStep()` + `httpPostWithRetry()` + content type 支援（workflow_exec.go）
8. `waitSingleCallback()`（workflow_exec.go）
9. `POST /api/callbacks/{key}` endpoint — 冪等 + 路徑 A/B + auth + audit（http_workflow.go）
10. `workflow_callbacks` DB table（workflow_exec.go）
11. `executeDAG()` 加 `resumeFrom` + `replayConditionResult()`（workflow_exec.go）
12. `rebuildContextFromRun()` + `resumeWorkflow()`（workflow_exec.go）
13. `recoverPendingWorkflows()` — 啟動時掃描 + timeout 檢查 + `isResume` flag（main.go）
14. Validation 規則 — 全部 + 靜態 callbackKey 碰撞 warning（workflow.go）
15. `hashWorkflow()` — run 記錄定義 hash，resume 比對（workflow_exec.go）
16. `runCancellers` map + cancel endpoint（http_workflow.go）
17. 測試：curl callback + resume + 冪等 + ResponseMapping + 碰撞 + cancel

### Phase 2：Streaming + Dashboard UI
18. `CallbackManager` streaming 模式（callback.go）
19. `waitStreamingCallback()` + DonePath/DoneValue 邏輯（workflow_exec.go）
20. DB schema streaming 支援 — sequence column（workflow_exec.go）
21. Editor 加 external step type（workflow-editor.js）
22. Property panel — 含 content type、raw body、response mapping、mode（workflow-editor.js）
23. Runs 面板 — waiting + streaming 狀態（workflow-editor.js）
24. Node 樣式 — 橘紅色 border（style.css）
25. SSE events — step_waiting, step_callback_received, workflow_status_changed

### Phase 3：安全 + 操作
26. Per-callback HMAC secret
27. `GET /api/callbacks` — pending callbacks 列表
28. Dashboard 手動 resolve 按鈕
29. Graceful shutdown — `srv.Shutdown()` 5s drain + cancel contexts（http_workflow.go）
30. XML callback 解析支援
31. Streaming callback DB 清理 — 已完成 run 的中間 records 7 天後清除（cron job）

---

## 測試計畫

### 手動測試 1：Echo callback（基本）
```bash
cat > ~/.tetora/workflows/test-external.json << 'EOF'
{
  "name": "test-external",
  "steps": [
    {
      "id": "step1", "type": "external",
      "externalUrl": "https://httpbin.org/post",
      "externalBody": {"test": "hello"},
      "callbackKey": "test-001", "callbackTimeout": "2m"
    },
    {
      "id": "step2", "type": "notify",
      "notifyMsg": "Result: {{steps.step1.output}}",
      "dependsOn": ["step1"]
    }
  ]
}
EOF

curl -X POST localhost:PORT/workflows/test-external/run -H "Authorization: Bearer TOKEN"
# → step1 進入 waiting

curl -X POST localhost:PORT/api/callbacks/test-001 \
  -H "Authorization: Bearer TOKEN" -H "Content-Type: application/json" \
  -d '{"status":"success","data":{"text":"hello world"}}'
# → step2 notify 包含結果
```

### 手動測試 2：ResponseMapping（Stripe 格式）
```bash
# callbackResponseMap: {statusPath: "type", dataPath: "data.object"}
# 送 Stripe 格式 webhook
curl -X POST localhost:PORT/api/callbacks/stripe-001 \
  -d '{"type":"charge.refunded","data":{"object":{"id":"re_xxx","amount":1234}}}'
# → step output = {"id":"re_xxx","amount":1234}（提取 data.object）
```

### 手動測試 3：結構化欄位存取
```bash
# step output = {"extracted_text":"發票","confidence":0.95}
# 下一步用 {{steps.ocr.output.extracted_text}} → "發票"
# 下一步用 {{steps.ocr.output.confidence}} → "0.95"
```

### 手動測試 4：Timeout
```bash
# callbackTimeout 設 10s，不送 callback
# onTimeout: "skip" → step "skipped"
# onTimeout: "stop" → step "error"
```

### 手動測試 5：Daemon restart + resume
```bash
curl -X POST localhost:PORT/workflows/test-external/run -H "Authorization: Bearer TOKEN"
kill $(pgrep tetora)
tetora daemon &
# 確認 log 有 "recovering workflow"
curl -X POST localhost:PORT/api/callbacks/test-001 -d '{"status":"success","data":{"recovered":true}}'
# response: {"status":"resumed"}
```

### 手動測試 6：重複 callback（冪等）
```bash
# 第一次：200 {"status": "delivered"}
# 第二次：409 {"error": "callback already delivered"}
```

### 手動測試 7：Streaming callback（Phase 2）
```bash
# callbackMode: "streaming", donePath: "status", doneValue: "final"
curl -X POST localhost:PORT/api/callbacks/lab-001 -d '{"status":"partial","results":[{"WBC":5.2}]}'
# → 200 {"status":"accumulated"}, step 繼續等

curl -X POST localhost:PORT/api/callbacks/lab-001 -d '{"status":"final","results":[{"WBC":5.2,"RBC":4.5}]}'
# → 200 {"status":"delivered"}, step 完成
```

### 手動測試 8：XML request + callback（Phase 3）
```bash
# externalContentType: "application/xml", externalRawBody: "<order>...</order>"
# callback 回 XML → 存為 raw string，下一步 agent 解析
```

### 手動測試 9：Condition + external resume
```bash
# condition → external (then branch) → notify
# Kill daemon during external wait → restart → callback → verify branch skip correct
```

### 手動測試 10：長時間等待（30d timeout）
```bash
# callbackTimeout: "14d"
# 驗證 validation 通過（< 30d），但 > 7d 會 log warning
```

---

## 場景驗證結果

### Review 2 — 5 場景（電商/HR/醫療/法律/製造）

| 場景 | Phase 1 後 | Phase 2 後 | 缺口 |
|------|-----------|-----------|------|
| 電商 Stripe 退款 | ✅ ResponseMapping 解決格式問題 | ✅ | — |
| 新人報到 Slack 審批 | ✅ ResponseMapping + 30d timeout | ✅ | — |
| 醫療檢驗報告 | ⚠️ 只能等最終結果 | ✅ Streaming 模式 | — |
| 法律合約審查 | ✅ 14d timeout + 結構化存取 | ✅ | 多輪修改需手動 re-trigger |
| 製造業品檢 MES | ⚠️ JSON 包 XML 字串 workaround | ✅ Phase 3 XML 原生支援 | — |

### Review 3 — 5 場景（SaaS/物流/金融/教育/IoT）

| 場景 | Phase 1 後 | Phase 2 後 | 缺口 |
|------|-----------|-----------|------|
| SaaS 用戶註冊驗證 | ✅ Single callback + ResponseMapping | ✅ | — |
| 物流包裹追蹤 | ✅ 陣列索引 `items.0.status` 已支援 | ✅ Streaming | — |
| 金融 KYC 身分驗證 | ✅ 巢狀欄位提取 + condition | ✅ | — |
| 教育作業批改 | ⚠️ output 非 JSON 時子欄位為空 | ✅ | 需確保前步 output 是 JSON |
| IoT 韌體更新 | ⚠️ buffer 256 + 非阻塞 send | ✅ Streaming + replay | — |

### Review 4 — 5 場景（政府/保險/影片/面試/內容審核）

| 場景 | Phase 1 後 | Phase 2 後 | 缺口 |
|------|-----------|-----------|------|
| 政府許可多層審批 | ⚠️ 只能等最終結果 | ✅ Streaming 追蹤各層級 | 30d 期間定義可能變（W3 hash 比對） |
| 保險理賠審查 | ✅ ResponseMapping + 結構化存取 | ✅ | — |
| 影片轉碼進度 | ⚠️ 只能等完成 | ✅ Streaming + DoneValue "100"（float64→string 轉換已支援） | — |
| 面試排程→回饋串接 | ✅ Chained external steps + resolveTemplateWithFields | ✅ | — |
| 內容審核分流 | ✅ Single callback + condition 分流 | ✅ | — |

### Review 5 — 5 場景（crash 恢復/長等待/並發失敗/shutdown/DB 膨脹）

| 場景 | Phase 1 後 | Phase 2 後 | 缺口 |
|------|-----------|-----------|------|
| POST 後 crash 丟失 callback | ✅ 先 DB 再 POST + post_sent flag | ✅ | — |
| 14 天法律審查 × 100 並發 | ✅ NewTimer + Stop 取代 time.After | ✅ | — |
| Parallel branch 失敗 + external 等待 | ✅ Coordinator 等全部結束再判定 | ✅ | — |
| 磁碟滿 checkpoint 失敗 | ⚠️ log error 繼續（可能 replay）| ✅ | Phase 3 可加 retry |
| Shutdown 期間 callback 到達 | ✅ srv.Shutdown 5s drain | ✅ | — |

### Review 6 — 5 場景（複合流程/串接/分支/多事件/累積）

| 場景 | Phase 1 後 | Phase 2 後 | 缺口 |
|------|-----------|-----------|------|
| 電商退貨 3 串接 external steps | ✅ resolveTemplateWithFields + post_sent | ✅ | — |
| 醫院掛號→AI問診→藥局（external+agent交替）| ✅ 正常 DAG 排序 | ✅ | — |
| Condition then/else 各有 external | ✅ replayConditionResult + DAG skip | ✅ | — |
| GitHub PR 多事件同 callbackKey | ⚠️ single 只收第一個 | ✅ streaming + boolean doneValue | — |
| IoT 批次校正 streaming timeout（需累積）| ⚠️ 只有最後一筆 | ✅ callbackAccumulate=true | — |

### Review 7 — 5 場景（競爭條件/第三方認證/重試/並發/空body）

| 場景 | Phase 1 後 | Phase 2 後 | 缺口 |
|------|-----------|-----------|------|
| 快速 callback（POST 回應前送達） | ✅ Register 在 POST 之前，channel 已建好 | ✅ | — |
| Stripe webhook（不帶 Bearer token） | ✅ callbackAuth: "open" 跳過驗證 | ✅ | — |
| External step retryMax=3 重試 | ✅ resetCallbackRecord 重設 DB + post_sent | ✅ | — |
| 3 並發 workflow 同 callbackKey 模板 | ✅ {{runId}} 展開後唯一 + Register 碰撞檢查 | ✅ | — |
| Callback body 空字串或無效 JSON | ✅ applyResponseMapping fallback 原始 body | ✅ | — |

### Review 8 — 5 場景（CI/CD/銀行/直播/合規/parallel+external）

| 場景 | Phase 1 後 | Phase 2 後 | 缺口 |
|------|-----------|-----------|------|
| CI/CD GitHub Actions webhook（open auth） | ✅ callbackAuth:open + ResponseMapping | ✅ | — |
| 銀行轉帳雙重 external（chained） | ✅ resolveTemplateWithFields + 各自 key | ✅ | — |
| 影片直播高頻 streaming（每秒 1 callback） | ⚠️ single 不支援 | ✅ buffer 256 + accumulate=false | — |
| 法規合規大 payload（接近 1MB） | ✅ MaxBytesReader 1MB + 深層 extractJSONPath | ✅ | — |
| 客戶回饋 parallel 內 external | ✅ 各自獨立 channel + coordinator 等全部 | ✅ | — |

### Review 9 — 5 場景（DocuSign/AI訓練/支付fallback/webhook relay/多租戶）

| 場景 | Phase 1 後 | Phase 2 後 | 缺口 |
|------|-----------|-----------|------|
| DocuSign 多簽署人（streaming 30d） | ⚠️ single 不支援 | ✅ streaming + accumulate + NewTimer | — |
| AI 模型訓練 48h + cancel | ⚠️ single 不支援 progress | ✅ streaming + cancel endpoint | — |
| 支付閘道 fallback（timeout→skip→condition） | ✅ onTimeout:skip + condition else | ✅ | — |
| Webhook relay chain（前步 output 作後步 body） | ✅ resolveTemplateWithFields 提取子欄位 | ✅ | — |
| 多租戶並發（callbackKey 含 tenant_id） | ✅ template 展開唯一 key + Register 碰撞檢查 | ✅ | — |

### Review 10 — 5 場景（審計/A-B測試/POST失敗/mixed mode/double crash）

| 場景 | Phase 1 後 | Phase 2 後 | 缺口 |
|------|-----------|-----------|------|
| 審計合規 error propagation | ✅ onTimeout:stop → DAG 中止後續 | ✅ | — |
| A/B 測試 condition → 不同 external | ✅ replayConditionResult 跳過 unchosen | ✅ | — |
| POST 全部失敗（3 次 retry） | ✅ httpPostWithRetry → step error + defer cleanup | ✅ | — |
| 混合 single + streaming 同 workflow | ✅ 各自獨立 channel/mode/buffer | ✅ | — |
| 二次 crash double resume | ✅ post_sent 持久 + 每次 resume 相同路徑 | ✅ | — |

**總計 45 場景：Phase 1 後 33/45 直接可用，12/45 需 Phase 2。Phase 2 後 44/45。Phase 3 後 45/45。**

**連續 3 輪（R8/R9/R10）新場景全數通過 → Spec 定案 ✅**

---

## Review 修正記錄

### Review 1（2026-03-10）— 架構與安全

| # | Severity | 問題 | 修正 |
|---|----------|------|------|
| C1 | Critical | Resume 遇到 condition 分支會 deadlock | `replayConditionResult()` |
| C2 | Critical | Orphaned channel 記憶體洩漏 | ctx cleanup + 1000 上限 |
| C3 | Critical | Checkpoint race condition | Status 只由 DAG coordinator 設定 |
| C4 | Critical | callbackKey SQL injection | 格式限制 `[a-zA-Z0-9._-]` |
| C5 | Critical | Callback endpoint 無認證 | Bearer token 必要 |
| W1 | Warning | 重複 callback 沒擋住 | DB 冪等檢查 |
| W2 | Warning | Recovery 沒檢查 timeout | `timeout_at` + `checkExpiredCallbacks()` |
| W3 | Warning | Parallel 內 external 未定義 | 文件化 |
| W5 | Warning | onTimeout: skip 語意不清 | skip→"skipped", stop→"error" |

### Review 2（2026-03-10）— 場景驗證（電商/HR/醫療/法律/製造）

| # | Gap | 修正 |
|---|-----|------|
| G1 | Webhook 格式不統一 | `callbackResponseMap` — statusPath/dataPath 提取 |
| G2 | Timeout 24h 太短 | 上限改 30d，> 7d log warning |
| G3 | 部分結果被冪等擋住 | `callbackMode: "streaming"` + DonePath/DoneValue |
| G4 | 不支援非 JSON 協議 | `externalContentType` + `externalRawBody` + `callbackContentType` |
| G5 | 無法存取 output 子欄位 | `{{steps.id.output.field}}` 點號語法 + `extractJSONPath()` |

### Review 3（2026-03-10）— 場景驗證（SaaS/物流/金融/教育/IoT）+ 技術深度

| # | Severity | 問題 | 修正 |
|---|----------|------|------|
| C1 | Critical | Streaming resume 丟失已累積的 callback | `ReplayAccumulated()` — 從 DB 載入送進 channel |
| C2 | Critical | `isCallbackDelivered()` composite key 查詢錯誤 | 加 sequence 參數：`isCallbackDelivered(key, 0)` |
| C3 | Critical | XML callback + ResponseMapping = 空白 output | Validation 擋：`callbackResponseMap only works with JSON` |
| C4 | Critical | Streaming buffer 16 太小，IoT 100+ callback deadlock | Buffer 改 256 + 非阻塞 send（5s timeout fallback to DB） |
| C5 | Critical | externalRawBody template var 破壞 XML 結構 | `resolveTemplateXMLEscaped()` — XML entity escape |
| C6 | Critical | `{{steps.id.output.field}}` 改核心 resolveTemplate 有回歸風險 | 隔離到 `resolveTemplateWithFields()`，Phase 1 不動核心 |
| W1 | Warning | extractJSONPath 不支援陣列索引 | 加 `[]any` case + `strconv.Atoi`，用 `items.0.id` 語法 |
| W2 | Warning | boolean 值比對失敗（JSON true vs string "true"）| extractJSONPath 對 bool/float64 做字串轉換 |
| W3 | Warning | ResponseMapping path 無格式驗證 | 加 `jsonPathRegex` 驗證 `[a-zA-Z0-9_.]` |

### Review 4（2026-03-10）— 場景驗證（政府/保險/影片/面試/內容審核）+ 並發與恢復

| # | Severity | 問題 | 修正 |
|---|----------|------|------|
| C1 | Critical | `Unregister()` double close → panic（defer + ctx goroutine 都呼叫）| `delete` 移入 `ok` 檢查內，二次呼叫時 key 不存在 → 跳過 |
| C2 | Critical | Resume 重送初始 HTTP POST → 重複退款/重複操作 | `isResume` flag + `hasPendingCallback()` → 跳過 POST |
| C3 | Critical | 靜態 callbackKey 並發碰撞 → channel 被覆蓋，前一 run 永遠卡住 | `Register()` 碰撞檢查 → nil → step error + validation warning |
| C4 | Critical | `isCallbackDelivered()` 函數簽名不一致（2 vs 3 參數）| 統一 3 參數 `(dbPath, key, sequence)` |
| C5 | Critical | Path B 無狀態檢查 → webhook 重試觸發重複 `resumeWorkflow()` | `queryPendingCallback` 限 `status='waiting'` + 先 mark 再 resume |
| W1 | Warning | 沒有取消等待中 workflow 的 API | `POST /workflow-runs/{id}/cancel` + `runCancellers` map |
| W2 | Warning | `runExternalStep()` 用 `resolveTemplate` 不支援子欄位 | 改用 `resolveTemplateWithFields()` + `resolveTemplateMapWithFields()` |
| W3 | Warning | 長等待期間 workflow 定義被修改 → resume 用新版不匹配 | `hashWorkflow()` 記入 run，resume 比對 → log warning |

### Review 5（2026-03-10）— Error Recovery、Performance、Shutdown

| # | Severity | 問題 | 修正 |
|---|----------|------|------|
| C1 | Critical | POST 與 DB 順序錯誤 → crash 丟失 callback | 先 DB（`post_sent=false`）再 POST → 更新 `post_sent=true`。Resume 檢查 flag |
| C2 | Critical | `time.After()` 長 timeout 記憶體洩漏（14 天 timer 永不 GC） | `time.NewTimer()` + `defer timer.Stop()` |
| W1 | Warning | Parallel branch 失敗 + external 等待中 — 語意不清 | Coordinator 等全部 step 結束再判定 workflow status |
| W2 | Warning | `checkpointRun()` 不檢查 DB 寫入錯誤 | 回傳 error + log，不中斷 workflow |
| W3 | Warning | Graceful shutdown 沒 drain period → callback 在 shutdown 期間 404 | `srv.Shutdown()` 5s drain 期 |
| W4 | Warning | Streaming callback DB 膨脹（IoT 500 × 100KB） | Phase 3 加 TTL 清理 cron job |

### Review 6（2026-03-10）— 複合流程 + Streaming 累積 + 整合重寫

| # | Severity | 問題 | 修正 |
|---|----------|------|------|
| C1 | Critical | Streaming timeout `lastOutput` 只保留最後一筆，丟失前 N-1 筆 | `accumulated []string` 累積所有結果 |
| C2 | Critical | Streaming 缺少累積模式 — 醫療(覆蓋)和 IoT(累積)需求不同 | `callbackAccumulate: bool` — true 合併為 JSON array，false 只保留最後 |
| — | 整合 | 6 輪修正造成 code 散亂、isResume/post_sent 衝突 | 全部核心區塊整合重寫（runExternalStep, waitStreaming, CallbackManager, DB Schema, Validation） |

### Review 7（2026-03-10）— 競爭條件 + 第三方認證 + 重試 + 整合重寫 v2

| # | Severity | 問題 | 修正 |
|---|----------|------|------|
| C1 | Critical | 快速 callback 競爭：POST 回應前 callback 到達 → 走 Path B 重複 DAG | Register channel 移至 POST 之前（步驟 3 < 步驟 5），確保 Path A 優先 |
| C2 | Critical | retryMax + external → DB unique constraint crash | `resetCallbackRecord()` 重設 status='waiting' + post_sent=0 |
| W1 | Warning | 第三方 webhook 無法帶 Bearer token（Stripe, GitHub） | `callbackAuth: "open"` — DB 記錄 auth_mode，HTTP handler 按 mode 決定是否驗證 |
| W2 | Warning | 並發 workflow 同靜態 callbackKey | callbackAuth="open" 強制要求 callbackKey 含 `{{runId}}`（validation 擋） |
| W3 | Warning | Callback body 空/無效 JSON → applyResponseMapping panic | 加空值檢查 + log warning，回傳原始 body |
| — | 整合 | R7 修正整合進核心流程 | HTTP handler 加 auth 分流、DB schema 加 auth_mode、Validation 加 callbackAuth 規則、邊界情況更新 |
