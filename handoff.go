package main

import (
	"context"
	"fmt"
	"time"

	"tetora/internal/handoff"
	"tetora/internal/log"
)

// --- Type aliases ---

type Handoff = handoff.Handoff
type AgentMessage = handoff.AgentMessage
type AutoDelegation = handoff.AutoDelegation

const maxAutoDelegations = handoff.MaxAutoDelegations

// --- Delegating functions ---

func initHandoffTables(dbPath string)                       { handoff.InitTables(dbPath) }
func recordHandoff(dbPath string, h Handoff) error          { return handoff.RecordHandoff(dbPath, h) }
func updateHandoffStatus(dbPath, id, status string) error   { return handoff.UpdateStatus(dbPath, id, status) }
func queryHandoffs(dbPath, wfID string) ([]Handoff, error)  { return handoff.QueryHandoffs(dbPath, wfID) }
func sendAgentMessage(dbPath string, msg AgentMessage) error {
	return handoff.SendAgentMessage(dbPath, msg, newUUID)
}
func queryAgentMessages(dbPath, wfID, role string, limit int) ([]AgentMessage, error) {
	return handoff.QueryAgentMessages(dbPath, wfID, role, limit)
}
func parseAutoDelegate(output string) []AutoDelegation { return handoff.ParseAutoDelegate(output) }
func findMatchingBrace(s string) int                   { return handoff.FindMatchingBrace(s) }
func buildHandoffPrompt(ctx, instr string) string      { return handoff.BuildHandoffPrompt(ctx, instr) }

// --- Execution (root-only: uses runSingleTask, dispatchState, sseBroker, etc.) ---

func executeHandoff(ctx context.Context, cfg *Config, h *Handoff,
	state *dispatchState, sem, childSem chan struct{}) TaskResult {

	prompt := buildHandoffPrompt(h.Context, h.Instruction)

	task := Task{
		ID:        newUUID(),
		Name:      fmt.Sprintf("handoff:%s→%s", h.FromAgent, h.ToAgent),
		Prompt:    prompt,
		Agent:     h.ToAgent,
		Source:    "handoff:" + h.FromAgent,
		SessionID: h.ToSessionID,
	}
	fillDefaults(cfg, &task)

	if task.Agent != "" {
		if soulPrompt, err := loadAgentPrompt(cfg, task.Agent); err == nil && soulPrompt != "" {
			task.SystemPrompt = soulPrompt
		}
	}

	now := time.Now().Format(time.RFC3339)
	createSession(cfg.HistoryDB, Session{
		ID:        task.SessionID,
		Agent:     h.ToAgent,
		Source:    "handoff:" + h.FromAgent,
		Status:    "active",
		Title:     fmt.Sprintf("Handoff from %s", h.FromAgent),
		CreatedAt: now,
		UpdatedAt: now,
	})

	h.Status = "active"
	updateHandoffStatus(cfg.HistoryDB, h.ID, "active")

	result := runSingleTask(ctx, cfg, task, sem, childSem, h.ToAgent)
	recordSessionActivity(cfg.HistoryDB, task, result, h.ToAgent)

	if result.Status == "success" {
		updateHandoffStatus(cfg.HistoryDB, h.ID, "completed")
	} else {
		updateHandoffStatus(cfg.HistoryDB, h.ID, "error")
	}

	if cfg.Log {
		log.Info("handoff completed", "from", h.FromAgent, "to", h.ToAgent, "handoff", h.ID[:8], "status", result.Status)
	}

	return result
}

func processAutoDelegations(ctx context.Context, cfg *Config, delegations []AutoDelegation,
	originalOutput, workflowRunID, fromAgent, fromStepID string,
	state *dispatchState, sem, childSem chan struct{}, broker *sseBroker) string {

	if len(delegations) == 0 {
		return originalOutput
	}

	combinedOutput := originalOutput

	for _, d := range delegations {
		if _, ok := cfg.Agents[d.Agent]; !ok {
			log.Warn("auto-delegate agent not found, skipping", "agent", d.Agent)
			continue
		}

		now := time.Now().Format(time.RFC3339)
		handoffID := newUUID()
		toSessionID := newUUID()

		h := Handoff{
			ID:            handoffID,
			WorkflowRunID: workflowRunID,
			FromAgent:     fromAgent,
			ToAgent:       d.Agent,
			FromStepID:    fromStepID,
			Context:       truncateStr(originalOutput, cfg.PromptBudget.ContextMaxOrDefault()),
			Instruction:   d.Task,
			Status:        "pending",
			ToSessionID:   toSessionID,
			CreatedAt:     now,
		}
		recordHandoff(cfg.HistoryDB, h)

		sendAgentMessage(cfg.HistoryDB, AgentMessage{
			WorkflowRunID: workflowRunID,
			FromAgent:     fromAgent,
			ToAgent:       d.Agent,
			Type:          "handoff",
			Content:       fmt.Sprintf("Auto-delegated: %s (reason: %s)", d.Task, d.Reason),
			RefID:         handoffID,
			CreatedAt:     now,
		})

		if broker != nil {
			broker.PublishMulti([]string{
				"workflow:" + workflowRunID,
			}, SSEEvent{
				Type: "auto_delegation",
				Data: map[string]any{
					"handoffId": handoffID,
					"fromAgent": fromAgent,
					"toAgent":   d.Agent,
					"task":      d.Task,
					"reason":    d.Reason,
				},
			})
		}

		if cfg.Log {
			log.Info("auto-delegate executing", "from", fromAgent, "to", d.Agent, "task", truncate(d.Task, 60))
		}

		result := executeHandoff(ctx, cfg, &h, state, sem, childSem)

		if result.Output != "" {
			combinedOutput += fmt.Sprintf("\n---\n[Delegated to %s]\n%s", d.Agent, result.Output)
		}

		sendAgentMessage(cfg.HistoryDB, AgentMessage{
			WorkflowRunID: workflowRunID,
			FromAgent:     d.Agent,
			ToAgent:       fromAgent,
			Type:          "response",
			Content:       truncateStr(result.Output, 2000),
			RefID:         handoffID,
			CreatedAt:     time.Now().Format(time.RFC3339),
		})
	}

	return combinedOutput
}

