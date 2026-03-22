---
title: "開始新的工作階段"
lang: zh-TW
date: "2026-03-20"
excerpt: "使用 tetora session new 開始全新 Agent 工作階段，不帶累積 context。"
---

隨著時間推移，Agent 工作階段會累積 context，可能拖慢回應速度或造成混亂。`tetora session` 指令幫你管理這些狀態。

## 建立新工作階段

```bash
tetora session new --agent kokuyou
```

為指定 Agent 開始全新工作階段，同時保留先前的工作階段在歷史記錄中。

## 什麼時候使用

- Agent 看起來混亂或引用舊 context
- 開始與先前工作完全無關的新任務
- 程式碼庫大幅變動後，先前 context 已失效
- 想要乾淨的環境來衡量 Agent 表現

## 工作階段管理

```bash
tetora session list                    # 列出所有工作階段
tetora session show <id>               # 查看工作階段詳情
tetora session switch <id>             # 切換到先前的工作階段
tetora session delete <id>             # 刪除工作階段
```

## 小技巧

- 工作階段是 per-agent 的——為一個 Agent 建立新工作階段不影響其他 Agent
- 先前的工作階段可透過 `tetora history` 搜尋
- 在設定中設置 `session.auto_rotate: true` 可每天自動建立新工作階段
