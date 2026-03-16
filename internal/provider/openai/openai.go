// Package openai implements the OpenAIProvider: executes tasks via OpenAI-compatible APIs.
package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"tetora/internal/provider"
)

// Provider executes tasks using OpenAI-compatible APIs.
// Supports OpenAI, Ollama, LM Studio, vLLM, and any compatible endpoint.
type Provider struct {
	name         string
	baseURL      string
	apiKey       string
	defaultModel string
}

// New creates a new OpenAI-compatible provider.
func New(name, baseURL, apiKey, defaultModel string) *Provider {
	return &Provider{
		name:         name,
		baseURL:      baseURL,
		apiKey:       apiKey,
		defaultModel: defaultModel,
	}
}

func (p *Provider) Name() string { return p.name }

// --- OpenAI request/response types ---

type toolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type toolCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function,omitempty"`
}

type tool struct {
	Type     string   `json:"type"`
	Function function `json:"function"`
}

type function struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type request struct {
	Model    string    `json:"model"`
	Messages []message `json:"messages"`
	Stream   bool      `json:"stream,omitempty"`
	Tools    []tool    `json:"tools,omitempty"`
}

type response struct {
	ID      string `json:"id"`
	Choices []struct {
		Message struct {
			Content   string     `json:"content"`
			ToolCalls []toolCall `json:"tool_calls,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

type streamChunk struct {
	ID      string `json:"id"`
	Choices []struct {
		Delta struct {
			Content   string          `json:"content"`
			ToolCalls []toolCallDelta `json:"tool_calls,omitempty"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

func (p *Provider) Execute(ctx context.Context, req provider.Request) (*provider.Result, error) {
	return p.executeInternal(ctx, req)
}

// ExecuteWithTools implements provider.ToolCapableProvider for multi-turn tool conversations.
func (p *Provider) ExecuteWithTools(ctx context.Context, req provider.Request) (*provider.Result, error) {
	return p.executeInternal(ctx, req)
}

func (p *Provider) executeInternal(ctx context.Context, req provider.Request) (*provider.Result, error) {
	model := req.Model
	if model == "" {
		model = p.defaultModel
	}
	if model == "" {
		return nil, fmt.Errorf("no model specified for provider %q", p.name)
	}

	var messages []message
	if req.SystemPrompt != "" {
		messages = append(messages, message{Role: "system", Content: req.SystemPrompt})
	}
	messages = append(messages, message{Role: "user", Content: req.Prompt})

	for _, m := range req.Messages {
		converted := convertMessages(m)
		messages = append(messages, converted...)
	}

	var tools []tool
	for _, t := range req.Tools {
		tools = append(tools, tool{
			Type: "function",
			Function: function{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}

	body := request{
		Model:    model,
		Messages: messages,
		Stream:   req.EventCh != nil,
		Tools:    tools,
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := p.baseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	start := time.Now()
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		elapsed := time.Since(start)
		return &provider.Result{
			IsError:    true,
			Error:      fmt.Sprintf("HTTP %d: %s", resp.StatusCode, truncate(respBody, 500)),
			DurationMs: elapsed.Milliseconds(),
		}, nil
	}

	if req.EventCh != nil {
		return p.readStreamResponse(resp.Body, req, start), nil
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	elapsed := time.Since(start)

	return ParseResponse(respBody, elapsed.Milliseconds()), nil
}

// convertMessages converts a provider.Message to one or more OpenAI messages.
func convertMessages(m provider.Message) []message {
	var blocks []provider.ContentBlock
	if err := json.Unmarshal(m.Content, &blocks); err == nil && len(blocks) > 0 {
		if m.Role == "assistant" {
			msg := message{Role: "assistant"}
			var textParts []string
			for _, b := range blocks {
				switch b.Type {
				case "text":
					textParts = append(textParts, b.Text)
				case "tool_use":
					tc := toolCall{ID: b.ID, Type: "function"}
					tc.Function.Name = b.Name
					tc.Function.Arguments = string(b.Input)
					msg.ToolCalls = append(msg.ToolCalls, tc)
				}
			}
			msg.Content = strings.Join(textParts, "\n")
			return []message{msg}
		}

		if m.Role == "user" {
			var msgs []message
			for _, b := range blocks {
				if b.Type == "tool_result" {
					msgs = append(msgs, message{
						Role:       "tool",
						Content:    b.Content,
						ToolCallID: b.ToolUseID,
					})
				}
			}
			if len(msgs) > 0 {
				return msgs
			}
		}
	}

	var s string
	if err := json.Unmarshal(m.Content, &s); err == nil {
		return []message{{Role: m.Role, Content: s}}
	}

	return []message{{Role: m.Role, Content: string(m.Content)}}
}

func (p *Provider) readStreamResponse(body io.Reader, req provider.Request, start time.Time) *provider.Result {
	scanner := bufio.NewScanner(body)
	var fullContent strings.Builder
	var sessionID string
	var tokensIn, tokensOut int
	var finishReason string

	type accumulator struct {
		id      string
		name    string
		argsBuf strings.Builder
	}
	var accs []accumulator

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if chunk.ID != "" && sessionID == "" {
			sessionID = chunk.ID
		}
		if chunk.Usage != nil {
			tokensIn = chunk.Usage.PromptTokens
			tokensOut = chunk.Usage.CompletionTokens
		}

		if len(chunk.Choices) > 0 {
			choice := chunk.Choices[0]
			if choice.FinishReason != nil {
				finishReason = *choice.FinishReason
			}

			delta := choice.Delta.Content
			if delta != "" {
				fullContent.WriteString(delta)
				if req.EventCh != nil {
					req.EventCh <- provider.Event{
						Type:      provider.EventOutputChunk,
						SessionID: req.SessionID,
						Data:      map[string]string{"chunk": delta},
						Timestamp: time.Now().Format(time.RFC3339),
					}
				}
			}

			for _, tcDelta := range choice.Delta.ToolCalls {
				idx := tcDelta.Index
				for len(accs) <= idx {
					accs = append(accs, accumulator{})
				}
				if tcDelta.ID != "" {
					accs[idx].id = tcDelta.ID
				}
				if tcDelta.Function.Name != "" {
					accs[idx].name = tcDelta.Function.Name
				}
				if tcDelta.Function.Arguments != "" {
					accs[idx].argsBuf.WriteString(tcDelta.Function.Arguments)
				}
			}
		}
	}

	elapsed := time.Since(start)

	var toolCalls []provider.ToolCall
	for _, acc := range accs {
		if acc.id != "" {
			toolCalls = append(toolCalls, provider.ToolCall{
				ID:    acc.id,
				Name:  acc.name,
				Input: json.RawMessage(acc.argsBuf.String()),
			})
		}
	}

	stopReason := MapFinishReason(finishReason)

	result := &provider.Result{
		Output:     fullContent.String(),
		DurationMs: elapsed.Milliseconds(),
		SessionID:  sessionID,
		TokensIn:   tokensIn,
		TokensOut:  tokensOut,
		ProviderMs: elapsed.Milliseconds(),
		ToolCalls:  toolCalls,
		StopReason: stopReason,
	}

	if tokensIn > 0 || tokensOut > 0 {
		result.CostUSD = EstimateCost(tokensIn, tokensOut)
	}

	return result
}

// ParseResponse parses an OpenAI-compatible API response.
func ParseResponse(data []byte, durationMs int64) *provider.Result {
	var resp response
	if err := json.Unmarshal(data, &resp); err != nil {
		return &provider.Result{
			IsError:    true,
			Error:      fmt.Sprintf("parse response: %v", err),
			DurationMs: durationMs,
		}
	}

	if resp.Error != nil {
		return &provider.Result{
			IsError:    true,
			Error:      resp.Error.Message,
			DurationMs: durationMs,
		}
	}

	result := &provider.Result{
		DurationMs: durationMs,
		SessionID:  resp.ID,
	}

	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		result.Output = choice.Message.Content

		for _, tc := range choice.Message.ToolCalls {
			result.ToolCalls = append(result.ToolCalls, provider.ToolCall{
				ID:    tc.ID,
				Name:  tc.Function.Name,
				Input: json.RawMessage(tc.Function.Arguments),
			})
		}

		result.StopReason = MapFinishReason(choice.FinishReason)
	}

	if resp.Usage != nil {
		result.TokensIn = resp.Usage.PromptTokens
		result.TokensOut = resp.Usage.CompletionTokens
		result.CostUSD = EstimateCost(resp.Usage.PromptTokens, resp.Usage.CompletionTokens)
	}

	result.ProviderMs = durationMs

	return result
}

// MapFinishReason converts OpenAI finish_reason to the normalized stop reason.
func MapFinishReason(reason string) string {
	switch reason {
	case "tool_calls":
		return "tool_use"
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "content_filter":
		return "content_filter"
	default:
		return reason
	}
}

// EstimateCost provides a rough cost estimate based on token counts.
// Uses approximate GPT-4o pricing as a default baseline.
func EstimateCost(promptTokens, completionTokens int) float64 {
	inputCost := float64(promptTokens) * 2.50 / 1_000_000
	outputCost := float64(completionTokens) * 10.00 / 1_000_000
	return inputCost + outputCost
}

func truncate(b []byte, maxLen int) string {
	s := string(b)
	if len(s) > maxLen {
		return s[:maxLen]
	}
	return s
}
