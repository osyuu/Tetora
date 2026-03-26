---
title: "0 Employees, 60 Clients — A Japanese Tax Accountant's AI Automation"
lang: en
date: "2026-03-18"
tag: case-study
readTime: "~8 min"
excerpt: "Japanese CPA Kento Hatakeyama serves 60 companies solo with Claude Code. The system he spent months building maps perfectly to Tetora's out-of-the-box features."
---

Japanese CPA and tax accountant Kento Hatakeyama recently published a long post on X that garnered over 2 million views.

His firm has **zero employees**, yet serves **60 client companies**. By industry standards, this workload requires 6 staff members and over 30 million yen (~$200K) in annual personnel costs. Using Claude Code, he built a comprehensive automation system that **saves over 24 hours per month — roughly 300 hours per year**.

It's an impressive case study. But what's even more interesting is that the system he spent months building from scratch maps almost perfectly to features Tetora already provides out of the box.

## What He Built

### 1. Nightly Auto-Journaling — Two-Stage AI Classification

A scheduled task runs at 21:00 every night, fetching unprocessed transaction records from the freee API for all 60 companies. Account classification uses a two-stage approach:

- **Stage 1: Keyword dictionary matching** — 14 account categories, 100+ keywords each. Fast and zero API cost
- **Stage 2: Claude API fallback** — Only unmatched transactions go to AI. A confidence threshold filters low-confidence results for human review

A mature design: don't waste AI on what rules can handle.

### 2. Five Services Connected via MCP

freee (accounting), Gmail, Google Calendar, Notion, Slack — all connected through MCP with Claude Code as the central orchestrator.

### 3. Skills: Accumulating "Business Patterns"

Repetitive business patterns defined as Claude Code Skills:

```
/freee-check    → Check unprocessed entries
/mtg-followup   → Post-meeting notes & action items
/ipo-analysis   → Analyze newly listed companies
```

The more you use them, the more Skills accumulate, the faster you work.

### 4. CLAUDE.md as the Business Manual

Journal entry rules, tax classifications, security policies, output paths, decision boundaries — all written into CLAUDE.md. A senior employee's SOP in a format AI can read.

### 5. Automated Task Logging

After each task: estimated manual time, actual AI time, and time saved are automatically logged. Monthly summaries are auto-generated.

### 6. Multi-Company Data Isolation

Data for all 60 companies is strictly isolated by company_id. Transaction details are logged only in company-specific log files.

## His Custom Build vs Tetora Out-of-the-Box

| Hatakeyama's Custom System | Tetora Equivalent | Notes |
|---|---|---|
| Nightly 21:00 scheduled execution | `tetora job add --cron "0 21 * * *"` | Built-in cron scheduler with full cron expression support |
| Claude Code Skills (/freee-check, etc.) | `tetora skill` system | Slash-command triggered, with version management |
| CLAUDE.md business manual | SOUL.md + CLAUDE.md | Each agent gets its own personality and rule files |
| Task logs (manual vs AI time tracking) | `tetora history` + Reflection | Auto-records cost, duration, and quality scores |
| MCP connections (freee/Gmail/Calendar) | `tetora mcp add` | Centralized MCP config, shared or per-agent |
| Multi-company data isolation (company_id) | Planned | Multi-tenant isolation in development |
| Two-stage AI classification (rules → AI fallback) | Definable in Workflow YAML | DAG workflows with conditional branching |

> **The key difference:** Hatakeyama spent months building schedulers, logging, and Skill management infrastructure from scratch. With Tetora, you skip all of that and **start writing business logic immediately**.

## Why "Field Knowledge" Is the Real Core

> What matters is knowing "what should be automated." The only person who can make that judgment is you — the one working on the ground every day.
> — Kento Hatakeyama

Hatakeyama emphasizes at the end of his post: it's precisely because he's not an engineer that Claude Code works so well. Tax accountants know the "business patterns" — journal entry rules, filing procedures, month-end checkpoints. This practical knowledge, accumulated over a decade, is something AI cannot generate on its own.

This is exactly Tetora's design philosophy.

Tetora doesn't require programming skills. What you need is **domain knowledge**:

- You know which processes can be standardized → Write them in **Workflow YAML**
- You know where the decision boundaries are → Write them in **SOUL.md**
- You know the repeating work patterns → Define them as **Skills**
- You know what to automate and what needs human eyes → Set the **permission level**

Engineers use AI to build "technically impressive things."
Professionals use AI to build "practically correct things."
**Tetora makes the latter possible — without building infrastructure from scratch.**
