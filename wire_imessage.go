package main

import (
	"context"
	"encoding/json"
	"fmt"

	imessagebot "tetora/internal/messaging/imessage"
)

// globalIMessageBot is the package-level iMessage bot instance.
var globalIMessageBot *imessagebot.Bot

func toolIMessageSend(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		ChatGUID string `json:"chat_guid"`
		Text     string `json:"text"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.ChatGUID == "" || args.Text == "" {
		return "", fmt.Errorf("chat_guid and text are required")
	}

	app := appFromCtx(ctx)
	if app == nil || app.IMessage == nil {
		return "", fmt.Errorf("iMessage bot not initialized")
	}

	if err := app.IMessage.SendMessage(args.ChatGUID, args.Text); err != nil {
		return "", err
	}
	return fmt.Sprintf("message sent to %s", args.ChatGUID), nil
}

func toolIMessageSearch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Query == "" {
		return "", fmt.Errorf("query is required")
	}
	if args.Limit <= 0 {
		args.Limit = 10
	}

	app := appFromCtx(ctx)
	if app == nil || app.IMessage == nil {
		return "", fmt.Errorf("iMessage bot not initialized")
	}

	messages, err := app.IMessage.SearchMessages(args.Query, args.Limit)
	if err != nil {
		return "", err
	}

	b, _ := json.Marshal(messages)
	return string(b), nil
}

func toolIMessageRead(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		ChatGUID string `json:"chat_guid"`
		Limit    int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.ChatGUID == "" {
		return "", fmt.Errorf("chat_guid is required")
	}
	if args.Limit <= 0 {
		args.Limit = 20
	}

	app := appFromCtx(ctx)
	if app == nil || app.IMessage == nil {
		return "", fmt.Errorf("iMessage bot not initialized")
	}

	messages, err := app.IMessage.ReadRecentMessages(args.ChatGUID, args.Limit)
	if err != nil {
		return "", err
	}

	b, _ := json.Marshal(messages)
	return string(b), nil
}
