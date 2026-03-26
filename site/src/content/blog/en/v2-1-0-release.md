---
title: "Tetora v2.1.0 — Massive Code Consolidation + Workflow Engine"
lang: en
date: "2026-03-18"
tag: release
readTime: "~5 min"
excerpt: "256 files consolidated to a lean core. New Workflow Engine with DAG support and Template Marketplace."
---

Tetora v2.1.0 is a significant release. Two themes define this version: **a major codebase restructure** and **new features shipping**.

For users: a more stable runtime, faster iteration cycles, and the long-awaited Workflow Engine and Template Marketplace. For developers: this is Tetora's transition from rapid prototype to long-term maintainable product.

> **TL;DR:** Root source files consolidated from 28 to 9, test files from 111 to 22, the entire repo compressed from 256+ files to a lean structure — while adding the Workflow Engine, Template Marketplace, and multiple Dashboard improvements.

## Codebase Consolidation: 256 Files → Lean Core

The consolidation happened across multiple refactoring rounds. Here are the final numbers:

| Metric | Before | After |
|---|---|---|
| Total repo files | 256+ | ~73 (ongoing) |
| Root source files | 28 | 9 |
| Test files | 111 | 22 |

The 9 root files are split by domain:

- `main.go` — Entry point, command routing, startup sequence
- `http.go` — HTTP server, API routing, Dashboard handlers
- `discord.go` — Discord gateway, message handling, terminal bridge
- `dispatch.go` — Task dispatch, TaskBoard, concurrency control
- `workflow.go` — Workflow Engine, DAG execution, step management
- `wire.go` — Cross-module wiring, initialization, dependency injection
- `tool.go` — Tool system, MCP integration, capabilities management
- `signal_unix.go` / `signal_windows.go` — Platform-specific signal handling

Substantial business logic has been migrated to `internal/` sub-packages: `internal/cron`, `internal/dispatch`, `internal/workflow`, `internal/taskboard`, `internal/reflection`, and more. The root layer now holds only a thin coordination layer.

### Why Does This Matter?

Previously, the root layer had 100+ files with functionality scattered everywhere — new contributors had to spend significant time just finding the right file. After consolidation:

- **Easier to maintain** — you know exactly where to look when changing a feature
- **Faster onboarding** — 9 entry points instead of 28
- **Clearer IDE navigation** — go to definition no longer gets lost in a sea of files
- **Faster builds** — fewer unnecessary package boundaries and import chains

## Workflow Engine

The Workflow Engine is the flagship new feature of v2.1.0. Define multi-step AI workflows in YAML; Tetora handles execution, error recovery, and state tracking.

### DAG-Based Pipeline

Workflows are defined as directed acyclic graphs (DAGs), supporting:

- **Conditional branching** — route execution based on the output of prior steps
- **Parallel steps** — steps with no dependencies run concurrently, reducing total wall time
- **Retry mechanism** — automatic retries on step failure, configurable count and backoff strategy

```yaml
name: content-pipeline
steps:
  - id: research
    agent: hisui
    prompt: "Research the topic: {{input.topic}}"
  - id: draft
    agent: kokuyou
    depends_on: [research]
    prompt: "Write a draft based on the research"
  - id: review
    agent: ruri
    depends_on: [draft]
    condition: "{{draft.word_count}} > 500"
```

### Dynamic Model Routing

The Workflow Engine automatically selects the model based on task complexity:

- Simple formatting, summarization → **Haiku** (fast, low cost)
- General reasoning, writing → **Sonnet** (default)
- Complex analysis, multi-step planning → **Opus** (maximum capability)

You can also specify the model explicitly in YAML, or let the router decide based on prompt length and keywords.

### Dashboard DAG Visualization

Running workflows are rendered as a node graph in the Dashboard: completed steps in green, running steps with a purple animation, waiting steps in gray, failed steps in red. Real-time pipeline progress at a glance — no need to tail logs.

## Template Marketplace

The Template Marketplace enables sharing, browsing, and one-click importing of workflow templates. It's Tetora's first step from a personal tool to a community ecosystem.

### Store Tab

A new Store tab in the Dashboard provides:

- **Category browsing** — filter by domain (Marketing, Engineering, Finance, Research, etc.)
- **Full-text search** — search across template names and descriptions
- **Featured section** — officially curated, high-quality templates
- **One-click import** — click to import directly into your local workspace

### Capabilities Tab

A new Capabilities tab gives a consolidated view of everything your Tetora instance can do:

- **Tools** — available MCP tool list
- **Skills** — defined Skill commands
- **Workflows** — local Workflow templates
- **Templates** — Agent prompt templates

### CLI Import / Export

Not just UI — full CLI support too:

```bash
tetora workflow export my-pipeline   # Export as shareable YAML
tetora workflow create from-store    # Import a template from the Store
tetora workflow list                  # List all local workflows
```

Exported YAML can be pasted directly into a GitHub Gist or the Tetora Store to share with the community.

## TaskBoard & Dispatch Improvements

Multiple important improvements to the TaskBoard and Dispatch layer improve stability and observability for multi-agent parallel workloads.

### Configurable Parallel Slots + Slot Pressure

You can now specify the maximum number of parallel slots and a slot pressure threshold in the config. When system load exceeds the threshold, new tasks are queued rather than force-inserted, preventing agents from competing for resources:

```json
{
  "dispatch": {
    "maxSlots": 4,
    "slotPressureThreshold": 0.8
  }
}
```

### Partial-Done Status

Long-running tasks now support a `partial-done` intermediate state. Agents can report progress after completing portions of work; the TaskBoard displays a completion percentage so you know the task is advancing rather than stalled.

### Worktree Data Protection

When multiple agents use Git worktrees for parallel development, explicit data isolation is now enforced. Each agent's working directory is independent — no accidental overwrites or merge conflicts polluting another agent's state.

### GitLab MR Support

In addition to GitHub PRs, GitLab Merge Request workflows are now supported. The `tetora pr create` command auto-detects the remote type and calls either the GitHub CLI or GitLab CLI accordingly to open the MR.

## Install / Upgrade

### Fresh Install

```bash
curl -fsSL https://tetora.dev/install.sh | bash
```

Single binary, zero external dependencies. macOS, Linux, and Windows all supported.

### Upgrading from a Prior Version

```bash
tetora upgrade
```

Automatically downloads the latest release, replaces the binary, and restarts the daemon. Tasks running at upgrade time are not interrupted.

> **Tip:** Before upgrading, confirm there are no long-running workflows in progress. Run `tetora status` to check active tasks.

## What's Next: v2.2 Roadmap

With v2.1.0 shipped, development focus shifts to two theme modules for v2.2:

### Financial Module

Personal and small business finance automation: income/expense tracking, report generation, budget monitoring. Integration with common accounting APIs (freee, Money Forward, etc.) is planned.

### Nutrition Module

Health and diet tracking: meal logging, nutritional analysis, goal setting. Claude acts as a nutrition advisor, offering personalized recommendations based on your eating habits.

Both modules will be published to the Store as Workflow templates — import and use immediately, no configuration from scratch required.

## Upgrade to v2.1.0 Now

Single binary, zero dependencies. macOS / Linux / Windows.

```bash
tetora upgrade
```

[View Release Notes on GitHub](https://github.com/TakumaLee/Tetora/releases)
