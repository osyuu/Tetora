---
title: "Discord Multi-Channel Setup"
lang: en
date: "2026-03-20"
excerpt: "Configure Tetora to manage multiple Discord channels with dedicated agents per channel."
---

Tetora supports assigning different agents to different Discord channels. This lets you create specialized channels — one for engineering tasks, another for content creation, etc.

## Configuration

In your `tetora.yml`, add channel mappings:

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

## How It Works

1. Each channel gets its own agent with independent context
2. Messages in that channel are routed to the assigned agent
3. Agents maintain separate conversation history per channel
4. You can override with `@agent-name` mentions in any channel

## Tips

- Use channel topics to remind users which agent is active
- Set up a `#dispatch` channel where Ruri (manager) can assign tasks across channels
- Each agent respects its own SOUL.md personality in channel conversations
