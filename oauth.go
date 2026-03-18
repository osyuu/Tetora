package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	tcrypto "tetora/internal/crypto"
	iOAuth "tetora/internal/oauth"
)

// --- Type aliases ---

type OAuthManager = iOAuth.OAuthManager
type OAuthToken = iOAuth.OAuthToken
type OAuthTokenStatus = iOAuth.OAuthTokenStatus

// globalOAuthManager is exposed for tool handlers (Gmail, Calendar, etc.) to make authenticated requests.
var globalOAuthManager *OAuthManager

// oauthTemplates provides built-in OAuth provider templates (alias to internal).
var oauthTemplates = iOAuth.OAuthTemplates

// --- Constructor ---

func newOAuthManager(cfg *Config) *OAuthManager {
	iOAuth.EncryptFn = tcrypto.Encrypt
	iOAuth.DecryptFn = tcrypto.Decrypt
	return iOAuth.NewOAuthManager(cfg.OAuth, cfg.HistoryDB, cfg.ListenAddr)
}

// --- Init ---

func initOAuthTable(dbPath string) error {
	return iOAuth.InitOAuthTable(dbPath)
}

// --- Encryption (used by tests) ---

func encryptOAuthToken(plaintext, key string) (string, error) {
	return tcrypto.Encrypt(plaintext, key)
}

func decryptOAuthToken(ciphertextHex, key string) (string, error) {
	return tcrypto.Decrypt(ciphertextHex, key)
}

// --- Token Storage (used by tests and http_admin) ---

func storeOAuthToken(dbPath string, token OAuthToken, encKey string) error {
	return iOAuth.StoreOAuthToken(dbPath, token, encKey)
}

func loadOAuthToken(dbPath, serviceName, encKey string) (*OAuthToken, error) {
	return iOAuth.LoadOAuthToken(dbPath, serviceName, encKey)
}

func deleteOAuthToken(dbPath, serviceName string) error {
	return iOAuth.DeleteOAuthToken(dbPath, serviceName)
}

func listOAuthTokenStatuses(dbPath, encKey string) ([]OAuthTokenStatus, error) {
	return iOAuth.ListOAuthTokenStatuses(dbPath, encKey)
}

// --- State (used by tests) ---

func generateState() (string, error) {
	return iOAuth.GenerateState()
}

// --- Tool Handlers ---

func toolOAuthStatus(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	statuses, err := listOAuthTokenStatuses(cfg.HistoryDB, cfg.OAuth.EncryptionKey)
	if err != nil {
		return "", fmt.Errorf("list oauth statuses: %w", err)
	}

	if len(statuses) == 0 {
		return "No OAuth services connected. Configure services in config.json under \"oauth.services\" and use the authorize endpoint to connect.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Connected OAuth services (%d):\n", len(statuses)))
	for _, s := range statuses {
		status := "connected"
		if s.ExpiresSoon {
			status = "expires soon"
		}
		sb.WriteString(fmt.Sprintf("  - %s: %s", s.ServiceName, status))
		if s.Scopes != "" {
			sb.WriteString(fmt.Sprintf(" (scopes: %s)", s.Scopes))
		}
		if s.ExpiresAt != "" {
			sb.WriteString(fmt.Sprintf(" (expires: %s)", s.ExpiresAt))
		}
		sb.WriteString("\n")
	}
	return sb.String(), nil
}

func toolOAuthRequest(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Service string `json:"service"`
		Method  string `json:"method"`
		URL     string `json:"url"`
		Body    string `json:"body"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("parse input: %w", err)
	}
	if args.Service == "" || args.URL == "" {
		return "", fmt.Errorf("service and url are required")
	}
	if args.Method == "" {
		args.Method = "GET"
	}

	app := appFromCtx(ctx)
	var mgr *OAuthManager
	if app != nil && app.OAuth != nil {
		mgr = app.OAuth
	} else {
		mgr = newOAuthManager(cfg)
	}
	var body io.Reader
	if args.Body != "" {
		body = strings.NewReader(args.Body)
	}

	resp, err := mgr.Request(ctx, args.Service, args.Method, args.URL, body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 100*1024))
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	return fmt.Sprintf("HTTP %d\n%s", resp.StatusCode, string(respBody)), nil
}

func toolOAuthAuthorize(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Service string `json:"service"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("parse input: %w", err)
	}
	if args.Service == "" {
		return "", fmt.Errorf("service is required")
	}

	app := appFromCtx(ctx)
	var mgr *OAuthManager
	if app != nil && app.OAuth != nil {
		mgr = app.OAuth
	} else {
		mgr = newOAuthManager(cfg)
	}
	svcCfg, err := mgr.ResolveServiceConfig(args.Service)
	if err != nil {
		return "", err
	}

	redirectURL := svcCfg.RedirectURL
	if redirectURL == "" {
		base := cfg.OAuth.RedirectBase
		if base == "" {
			base = "http://localhost" + cfg.ListenAddr
		}
		redirectURL = base + "/api/oauth/" + args.Service + "/callback"
	}

	params := url.Values{
		"client_id":     {svcCfg.ClientID},
		"redirect_uri":  {redirectURL},
		"response_type": {"code"},
	}
	if len(svcCfg.Scopes) > 0 {
		params.Set("scope", strings.Join(svcCfg.Scopes, " "))
	}
	for k, v := range svcCfg.ExtraParams {
		params.Set(k, v)
	}

	authorizeURL := fmt.Sprintf("%s/api/oauth/%s/authorize", strings.TrimRight(cfg.OAuth.RedirectBase, "/"), args.Service)
	if cfg.OAuth.RedirectBase == "" {
		authorizeURL = fmt.Sprintf("http://localhost%s/api/oauth/%s/authorize", cfg.ListenAddr, args.Service)
	}

	return fmt.Sprintf("To connect %s, visit this URL:\n%s\n\nThe authorization flow will handle CSRF protection and token exchange automatically.", args.Service, authorizeURL), nil
}

// Ensure *http.Response is used so the import is not flagged unused.
var _ *http.Response
