---
title: "Discord 多頻道設定"
lang: zh-TW
date: "2026-03-20"
excerpt: "設定 Tetora 管理多個 Discord 頻道，每個頻道指定專屬 Agent。"
---

Tetora 支援為不同 Discord 頻道指派不同 Agent。這讓你可以建立專門頻道——一個處理工程任務、另一個處理內容創作等。

## 設定方式

在 `tetora.yml` 中新增頻道對應：

```yaml
discord:
  channels:
    "engineering":
      agent: kokuyou
      prefix: "!"
    "content":
      agent: kohaku
      prefix: "!"
    "general":
      agent: ruri
      prefix: "!"
```

## 運作方式

1. 每個頻道有自己的 Agent 與獨立 context
2. 頻道中的訊息會路由到指定 Agent
3. Agent 在每個頻道維護獨立的對話歷史
4. 在任何頻道中可用 `@agent-name` 覆蓋預設 Agent

## 小技巧

- 用頻道主題提醒使用者目前哪個 Agent 在線
- 設一個 `#dispatch` 頻道讓琉璃（管理者）跨頻道分配任務
- 每個 Agent 在頻道對話中遵循自己的 SOUL.md 個性
