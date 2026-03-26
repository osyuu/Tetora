package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// WebSearchConfig holds configuration for web search.
type WebSearchConfig struct {
	Provider   string `json:"provider"`
	APIKey     string `json:"apiKey"`
	BaseURL    string `json:"baseURL"`
	MaxResults int    `json:"maxResults"`
}

// WebSearch performs web search using the configured search provider.
func WebSearch(ctx context.Context, cfg WebSearchConfig, input json.RawMessage) (string, error) {
	var args struct {
		Query      string `json:"query"`
		MaxResults int    `json:"maxResults"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Query == "" {
		return "", fmt.Errorf("query is required")
	}
	if args.MaxResults <= 0 {
		args.MaxResults = cfg.MaxResults
		if args.MaxResults <= 0 {
			args.MaxResults = 5
		}
	}

	if cfg.Provider == "" {
		return "", fmt.Errorf("web search not configured (set tools.webSearch.provider in config)")
	}

	switch cfg.Provider {
	case "brave":
		return searchBrave(ctx, cfg, args.Query, args.MaxResults)
	case "tavily":
		return searchTavily(ctx, cfg, args.Query, args.MaxResults)
	case "searxng":
		return searchSearXNG(ctx, cfg, args.Query, args.MaxResults)
	default:
		return "", fmt.Errorf("unknown search provider: %s", cfg.Provider)
	}
}

func searchBrave(ctx context.Context, cfg WebSearchConfig, query string, maxResults int) (string, error) {
	if cfg.APIKey == "" {
		return "", fmt.Errorf("brave search requires apiKey in tools.webSearch")
	}

	url := fmt.Sprintf("https://api.search.brave.com/res/v1/web/search?q=%s&count=%d",
		URLEncode(query), maxResults)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", cfg.APIKey)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("brave api error: %d %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	var braveResp struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(body, &braveResp); err != nil {
		return "", fmt.Errorf("parse brave response: %w", err)
	}

	var results []map[string]string
	for _, r := range braveResp.Web.Results {
		results = append(results, map[string]string{
			"title":   r.Title,
			"url":     r.URL,
			"snippet": r.Description,
		})
	}

	out, _ := json.Marshal(results)
	return string(out), nil
}

func searchTavily(ctx context.Context, cfg WebSearchConfig, query string, maxResults int) (string, error) {
	if cfg.APIKey == "" {
		return "", fmt.Errorf("tavily search requires apiKey in tools.webSearch")
	}

	reqBody := map[string]any{
		"query":               query,
		"max_results":         maxResults,
		"search_depth":        "basic",
		"include_answer":      false,
		"include_raw_content": false,
	}
	reqJSON, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.tavily.com/search", strings.NewReader(string(reqJSON)))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", cfg.APIKey)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("tavily api error: %d %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	var tavilyResp struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &tavilyResp); err != nil {
		return "", fmt.Errorf("parse tavily response: %w", err)
	}

	var results []map[string]string
	for _, r := range tavilyResp.Results {
		results = append(results, map[string]string{
			"title":   r.Title,
			"url":     r.URL,
			"snippet": r.Content,
		})
	}

	out, _ := json.Marshal(results)
	return string(out), nil
}

func searchSearXNG(ctx context.Context, cfg WebSearchConfig, query string, maxResults int) (string, error) {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		return "", fmt.Errorf("searxng requires baseURL in tools.webSearch")
	}

	url := fmt.Sprintf("%s/search?q=%s&format=json&pageno=1", baseURL, URLEncode(query))

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("searxng api error: %d %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	var searxResp struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &searxResp); err != nil {
		return "", fmt.Errorf("parse searxng response: %w", err)
	}

	if len(searxResp.Results) > maxResults {
		searxResp.Results = searxResp.Results[:maxResults]
	}

	var results []map[string]string
	for _, r := range searxResp.Results {
		results = append(results, map[string]string{
			"title":   r.Title,
			"url":     r.URL,
			"snippet": r.Content,
		})
	}

	out, _ := json.Marshal(results)
	return string(out), nil
}

// URLEncode encodes a string for use in URL query parameters.
func URLEncode(s string) string {
	s = strings.ReplaceAll(s, " ", "+")
	s = strings.ReplaceAll(s, "&", "%26")
	s = strings.ReplaceAll(s, "=", "%3D")
	s = strings.ReplaceAll(s, "?", "%3F")
	s = strings.ReplaceAll(s, "#", "%23")
	return s
}
