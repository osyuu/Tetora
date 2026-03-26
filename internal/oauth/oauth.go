// Package oauth implements the OAuth 2.0 generic framework (P18.2).
// Token storage, refresh, and HTTP routing for multiple OAuth providers.
package oauth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"tetora/internal/config"
	"tetora/internal/db"
)

// --- Encryption hooks (set by root wire file) ---

// EncryptFn encrypts plaintext before DB storage. Nil = no encryption.
var EncryptFn func(plaintext, key string) (string, error)

// DecryptFn decrypts ciphertext after DB retrieval. Nil = no decryption.
var DecryptFn func(ciphertext, key string) (string, error)

// --- Config Types (aliased from internal/config) ---

type OAuthConfig = config.OAuthConfig
type OAuthServiceConfig = config.OAuthServiceConfig

// OAuthToken represents a stored token.
type OAuthToken struct {
	ServiceName  string `json:"serviceName"`
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken,omitempty"`
	TokenType    string `json:"tokenType,omitempty"`
	ExpiresAt    string `json:"expiresAt,omitempty"`
	Scopes       string `json:"scopes,omitempty"`
	CreatedAt    string `json:"createdAt"`
	UpdatedAt    string `json:"updatedAt"`
}

// OAuthTokenStatus is a public-safe view of token status (no secrets).
type OAuthTokenStatus struct {
	ServiceName string `json:"serviceName"`
	Connected   bool   `json:"connected"`
	Scopes      string `json:"scopes,omitempty"`
	ExpiresAt   string `json:"expiresAt,omitempty"`
	ExpiresSoon bool   `json:"expiresSoon,omitempty"`
	CreatedAt   string `json:"createdAt,omitempty"`
}

// OAuthManager coordinates OAuth flows and token lifecycle.
type OAuthManager struct {
	oauthCfg      OAuthConfig
	dbPath        string
	listenAddr    string
	encryptionKey string
	states        map[string]oauthState // CSRF state token -> service info
	mu            sync.Mutex
}

type oauthState struct {
	service   string
	createdAt time.Time
}

// oauthTokenResponse is the JSON response from a token endpoint.
type oauthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
	ExpiresIn    int    `json:"expires_in,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

// OAuthTemplates provides built-in OAuth provider templates.
var OAuthTemplates = map[string]OAuthServiceConfig{
	"google": {
		AuthURL:     "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL:    "https://oauth2.googleapis.com/token",
		ExtraParams: map[string]string{"access_type": "offline", "prompt": "consent"},
	},
	"github": {
		AuthURL:  "https://github.com/login/oauth/authorize",
		TokenURL: "https://github.com/login/oauth/access_token",
	},
	"twitter": {
		AuthURL:  "https://twitter.com/i/oauth2/authorize",
		TokenURL: "https://api.twitter.com/2/oauth2/token",
	},
}

// NewOAuthManager creates a new OAuthManager from explicit parameters.
func NewOAuthManager(oauthCfg OAuthConfig, dbPath, listenAddr string) *OAuthManager {
	return &OAuthManager{
		oauthCfg:      oauthCfg,
		dbPath:        dbPath,
		listenAddr:    listenAddr,
		encryptionKey: oauthCfg.EncryptionKey,
		states:        make(map[string]oauthState),
	}
}

// InitOAuthTable creates the oauth_tokens table.
func InitOAuthTable(dbPath string) error {
	sql := `CREATE TABLE IF NOT EXISTS oauth_tokens (
		service_name TEXT PRIMARY KEY,
		access_token TEXT NOT NULL,
		refresh_token TEXT DEFAULT '',
		token_type TEXT DEFAULT 'Bearer',
		expires_at TEXT DEFAULT '',
		scopes TEXT DEFAULT '',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);`
	_, err := db.Query(dbPath, sql)
	return err
}

// --- Token Encryption ---

// EncryptOAuthToken encrypts plaintext using the configured EncryptFn.
func EncryptOAuthToken(plaintext, key string) (string, error) {
	if EncryptFn == nil {
		return plaintext, nil
	}
	return EncryptFn(plaintext, key)
}

// DecryptOAuthToken decrypts ciphertext using the configured DecryptFn.
func DecryptOAuthToken(ciphertextHex, key string) (string, error) {
	if DecryptFn == nil {
		return ciphertextHex, nil
	}
	return DecryptFn(ciphertextHex, key)
}

// --- Token Storage ---

// StoreOAuthToken stores (or updates) an OAuth token in the DB.
// Access and refresh tokens are encrypted if an encryption key is set.
func StoreOAuthToken(dbPath string, token OAuthToken, encKey string) error {
	accessEnc, err := EncryptOAuthToken(token.AccessToken, encKey)
	if err != nil {
		return fmt.Errorf("encrypt access_token: %w", err)
	}
	refreshEnc, err := EncryptOAuthToken(token.RefreshToken, encKey)
	if err != nil {
		return fmt.Errorf("encrypt refresh_token: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if token.CreatedAt == "" {
		token.CreatedAt = now
	}
	token.UpdatedAt = now

	sql := fmt.Sprintf(
		`INSERT OR REPLACE INTO oauth_tokens (service_name, access_token, refresh_token, token_type, expires_at, scopes, created_at, updated_at) VALUES ('%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s')`,
		db.Escape(token.ServiceName),
		db.Escape(accessEnc),
		db.Escape(refreshEnc),
		db.Escape(token.TokenType),
		db.Escape(token.ExpiresAt),
		db.Escape(token.Scopes),
		db.Escape(token.CreatedAt),
		db.Escape(token.UpdatedAt),
	)
	_, err = db.Query(dbPath, sql)
	return err
}

// LoadOAuthToken loads and decrypts a token from the DB.
func LoadOAuthToken(dbPath, serviceName, encKey string) (*OAuthToken, error) {
	sql := fmt.Sprintf(
		`SELECT service_name, access_token, refresh_token, token_type, expires_at, scopes, created_at, updated_at FROM oauth_tokens WHERE service_name = '%s'`,
		db.Escape(serviceName),
	)
	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}

	row := rows[0]
	accessDec, err := DecryptOAuthToken(fmt.Sprint(row["access_token"]), encKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt access_token: %w", err)
	}
	refreshDec, err := DecryptOAuthToken(fmt.Sprint(row["refresh_token"]), encKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt refresh_token: %w", err)
	}

	return &OAuthToken{
		ServiceName:  fmt.Sprint(row["service_name"]),
		AccessToken:  accessDec,
		RefreshToken: refreshDec,
		TokenType:    fmt.Sprint(row["token_type"]),
		ExpiresAt:    fmt.Sprint(row["expires_at"]),
		Scopes:       fmt.Sprint(row["scopes"]),
		CreatedAt:    fmt.Sprint(row["created_at"]),
		UpdatedAt:    fmt.Sprint(row["updated_at"]),
	}, nil
}

// DeleteOAuthToken removes a token from the DB.
func DeleteOAuthToken(dbPath, serviceName string) error {
	sql := fmt.Sprintf(
		`DELETE FROM oauth_tokens WHERE service_name = '%s'`,
		db.Escape(serviceName),
	)
	_, err := db.Query(dbPath, sql)
	return err
}

// ListOAuthTokenStatuses returns status info for all stored tokens (no secrets).
func ListOAuthTokenStatuses(dbPath, encKey string) ([]OAuthTokenStatus, error) {
	rows, err := db.Query(dbPath, `SELECT service_name, expires_at, scopes, created_at FROM oauth_tokens ORDER BY service_name`)
	if err != nil {
		return nil, err
	}

	statuses := make([]OAuthTokenStatus, 0, len(rows))
	for _, row := range rows {
		expiresAt := fmt.Sprint(row["expires_at"])
		expiresSoon := false
		if expiresAt != "" {
			if t, err := time.Parse(time.RFC3339, expiresAt); err == nil {
				expiresSoon = time.Until(t) < 5*time.Minute
			}
		}
		statuses = append(statuses, OAuthTokenStatus{
			ServiceName: fmt.Sprint(row["service_name"]),
			Connected:   true,
			Scopes:      fmt.Sprint(row["scopes"]),
			ExpiresAt:   expiresAt,
			ExpiresSoon: expiresSoon,
			CreatedAt:   fmt.Sprint(row["created_at"]),
		})
	}
	return statuses, nil
}

// --- Token Refresh ---

// RefreshTokenIfNeeded checks token expiry and refreshes if needed.
func (m *OAuthManager) RefreshTokenIfNeeded(serviceName string) (*OAuthToken, error) {
	return m.refreshTokenIfNeeded(serviceName)
}

func (m *OAuthManager) refreshTokenIfNeeded(serviceName string) (*OAuthToken, error) {
	token, err := LoadOAuthToken(m.dbPath, serviceName, m.encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("load token: %w", err)
	}
	if token == nil {
		return nil, fmt.Errorf("no token stored for service %q", serviceName)
	}

	// Check if token is still valid.
	if token.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, token.ExpiresAt); err == nil {
			if time.Until(t) > 60*time.Second {
				return token, nil // still valid
			}
		}
	}

	// No refresh token — return current token as-is.
	if token.RefreshToken == "" {
		slog.Debug("oauth token expired but no refresh_token", "service", serviceName)
		return token, nil
	}

	// Resolve service config.
	svcCfg, err := m.ResolveServiceConfig(serviceName)
	if err != nil {
		return nil, err
	}

	// Exchange refresh token for new access token.
	slog.Info("oauth refreshing token", "service", serviceName)
	data := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {token.RefreshToken},
		"client_id":     {svcCfg.ClientID},
		"client_secret": {svcCfg.ClientSecret},
	}

	resp, err := http.PostForm(svcCfg.TokenURL, data)
	if err != nil {
		return nil, fmt.Errorf("refresh request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("refresh failed (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var tokenResp oauthTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse refresh response: %w", err)
	}

	// Update token.
	token.AccessToken = tokenResp.AccessToken
	if tokenResp.RefreshToken != "" {
		token.RefreshToken = tokenResp.RefreshToken // some providers rotate refresh tokens
	}
	if tokenResp.TokenType != "" {
		token.TokenType = tokenResp.TokenType
	}
	if tokenResp.ExpiresIn > 0 {
		token.ExpiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second).UTC().Format(time.RFC3339)
	}

	if err := StoreOAuthToken(m.dbPath, *token, m.encryptionKey); err != nil {
		return nil, fmt.Errorf("store refreshed token: %w", err)
	}

	slog.Info("oauth token refreshed", "service", serviceName, "expiresAt", token.ExpiresAt)
	return token, nil
}

// --- Authenticated HTTP Request ---

// Request makes an authenticated HTTP request using the stored token for the given service.
// It auto-refreshes the token if needed.
func (m *OAuthManager) Request(ctx context.Context, serviceName, method, reqURL string, body io.Reader) (*http.Response, error) {
	token, err := m.refreshTokenIfNeeded(serviceName)
	if err != nil {
		return nil, fmt.Errorf("oauth token: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	tokenType := token.TokenType
	if tokenType == "" {
		tokenType = "Bearer"
	}
	req.Header.Set("Authorization", tokenType+" "+token.AccessToken)

	return http.DefaultClient.Do(req)
}

// --- Authorization Flow ---

// cleanExpiredStates removes CSRF states older than 10 minutes.
func (m *OAuthManager) cleanExpiredStates() {
	cutoff := time.Now().Add(-10 * time.Minute)
	for k, v := range m.states {
		if v.createdAt.Before(cutoff) {
			delete(m.states, k)
		}
	}
}

// GenerateState creates a random CSRF state token.
func GenerateState() (string, error) {
	b := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// HandleAuthorize starts an OAuth authorization flow — redirects the user to the provider.
func (m *OAuthManager) HandleAuthorize(w http.ResponseWriter, r *http.Request, serviceName string) {
	svcCfg, err := m.ResolveServiceConfig(serviceName)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
		return
	}

	state, err := GenerateState()
	if err != nil {
		http.Error(w, `{"error":"state generation failed"}`, http.StatusInternalServerError)
		return
	}

	m.mu.Lock()
	m.cleanExpiredStates()
	m.states[state] = oauthState{service: serviceName, createdAt: time.Now()}
	m.mu.Unlock()

	// Build redirect URL.
	redirectURL := svcCfg.RedirectURL
	if redirectURL == "" {
		base := m.oauthCfg.RedirectBase
		if base == "" {
			base = "http://localhost" + m.listenAddr
		}
		redirectURL = base + "/api/oauth/" + serviceName + "/callback"
	}

	params := url.Values{
		"client_id":     {svcCfg.ClientID},
		"redirect_uri":  {redirectURL},
		"response_type": {"code"},
		"state":         {state},
	}
	if len(svcCfg.Scopes) > 0 {
		params.Set("scope", strings.Join(svcCfg.Scopes, " "))
	}
	for k, v := range svcCfg.ExtraParams {
		params.Set(k, v)
	}

	authURL := svcCfg.AuthURL + "?" + params.Encode()
	slog.Info("oauth authorize redirect", "service", serviceName, "url", authURL)

	http.Redirect(w, r, authURL, http.StatusFound)
}

// HandleCallback processes the OAuth callback from the provider.
func (m *OAuthManager) HandleCallback(w http.ResponseWriter, r *http.Request, serviceName string) {
	// Validate CSRF state.
	state := r.URL.Query().Get("state")
	if state == "" {
		http.Error(w, `{"error":"missing state parameter"}`, http.StatusBadRequest)
		return
	}

	m.mu.Lock()
	st, ok := m.states[state]
	if ok {
		delete(m.states, state) // consume state
	}
	m.mu.Unlock()

	if !ok {
		http.Error(w, `{"error":"invalid or expired state"}`, http.StatusBadRequest)
		return
	}
	if st.service != serviceName {
		http.Error(w, `{"error":"state service mismatch"}`, http.StatusBadRequest)
		return
	}
	if time.Since(st.createdAt) > 10*time.Minute {
		http.Error(w, `{"error":"state expired"}`, http.StatusBadRequest)
		return
	}

	// Check for error from provider.
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		errDesc := r.URL.Query().Get("error_description")
		http.Error(w, fmt.Sprintf(`{"error":"%s","description":"%s"}`, errParam, errDesc), http.StatusBadRequest)
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, `{"error":"missing authorization code"}`, http.StatusBadRequest)
		return
	}

	svcCfg, err := m.ResolveServiceConfig(serviceName)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
		return
	}

	// Build redirect URL (must match the one used in authorize).
	redirectURL := svcCfg.RedirectURL
	if redirectURL == "" {
		base := m.oauthCfg.RedirectBase
		if base == "" {
			base = "http://localhost" + m.listenAddr
		}
		redirectURL = base + "/api/oauth/" + serviceName + "/callback"
	}

	// Exchange code for token.
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURL},
		"client_id":     {svcCfg.ClientID},
		"client_secret": {svcCfg.ClientSecret},
	}

	req, err := http.NewRequest("POST", svcCfg.TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"request creation: %v"}`, err), http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"token exchange: %v"}`, err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		slog.Warn("oauth token exchange failed", "service", serviceName, "status", resp.StatusCode, "body", string(body))
		http.Error(w, fmt.Sprintf(`{"error":"token exchange failed (HTTP %d)"}`, resp.StatusCode), http.StatusBadGateway)
		return
	}

	var tokenResp oauthTokenResponse
	// Some providers (like GitHub) may return as form-encoded.
	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/x-www-form-urlencoded") || strings.Contains(contentType, "text/plain") {
		vals, err := url.ParseQuery(string(body))
		if err == nil {
			tokenResp.AccessToken = vals.Get("access_token")
			tokenResp.RefreshToken = vals.Get("refresh_token")
			tokenResp.TokenType = vals.Get("token_type")
			tokenResp.Scope = vals.Get("scope")
		}
	} else {
		if err := json.Unmarshal(body, &tokenResp); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"parse token response: %v"}`, err), http.StatusBadGateway)
			return
		}
	}

	if tokenResp.AccessToken == "" {
		http.Error(w, `{"error":"no access_token in response"}`, http.StatusBadGateway)
		return
	}

	// Build token.
	now := time.Now().UTC()
	token := OAuthToken{
		ServiceName:  serviceName,
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		TokenType:    tokenResp.TokenType,
		Scopes:       tokenResp.Scope,
		CreatedAt:    now.Format(time.RFC3339),
		UpdatedAt:    now.Format(time.RFC3339),
	}
	if token.TokenType == "" {
		token.TokenType = "Bearer"
	}
	if tokenResp.ExpiresIn > 0 {
		token.ExpiresAt = now.Add(time.Duration(tokenResp.ExpiresIn) * time.Second).Format(time.RFC3339)
	}
	if tokenResp.Scope == "" && len(svcCfg.Scopes) > 0 {
		token.Scopes = strings.Join(svcCfg.Scopes, " ")
	}

	// Store token.
	if m.encryptionKey == "" {
		slog.Warn("oauth storing token WITHOUT encryption — set oauth.encryptionKey for security", "service", serviceName)
	}
	if err := StoreOAuthToken(m.dbPath, token, m.encryptionKey); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"store token: %v"}`, err), http.StatusInternalServerError)
		return
	}

	slog.Info("oauth token stored", "service", serviceName, "expiresAt", token.ExpiresAt)

	// Return success HTML.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html><html><body style="font-family:system-ui;text-align:center;margin-top:80px">
<h2>OAuth Connected</h2>
<p>Service <strong>%s</strong> has been connected successfully.</p>
<p>You can close this window.</p>
<script>setTimeout(function(){window.close()},3000)</script>
</body></html>`, serviceName)
}

// --- Service Config Resolution ---

// ResolveServiceConfig merges built-in templates with user-provided config.
func (m *OAuthManager) ResolveServiceConfig(name string) (*OAuthServiceConfig, error) {
	userCfg, hasUser := m.oauthCfg.Services[name]
	tmpl, hasTmpl := OAuthTemplates[name]

	if !hasUser && !hasTmpl {
		return nil, fmt.Errorf("unknown oauth service %q: not configured and no built-in template", name)
	}

	// Start from template if available.
	result := OAuthServiceConfig{Name: name}
	if hasTmpl {
		result.AuthURL = tmpl.AuthURL
		result.TokenURL = tmpl.TokenURL
		if tmpl.ExtraParams != nil {
			result.ExtraParams = make(map[string]string)
			for k, v := range tmpl.ExtraParams {
				result.ExtraParams[k] = v
			}
		}
	}

	// Override with user config.
	if hasUser {
		if userCfg.ClientID != "" {
			result.ClientID = userCfg.ClientID
		}
		if userCfg.ClientSecret != "" {
			result.ClientSecret = userCfg.ClientSecret
		}
		if userCfg.AuthURL != "" {
			result.AuthURL = userCfg.AuthURL
		}
		if userCfg.TokenURL != "" {
			result.TokenURL = userCfg.TokenURL
		}
		if len(userCfg.Scopes) > 0 {
			result.Scopes = userCfg.Scopes
		}
		if userCfg.RedirectURL != "" {
			result.RedirectURL = userCfg.RedirectURL
		}
		if userCfg.ExtraParams != nil {
			if result.ExtraParams == nil {
				result.ExtraParams = make(map[string]string)
			}
			for k, v := range userCfg.ExtraParams {
				result.ExtraParams[k] = v
			}
		}
	}

	// Validate required fields.
	if result.ClientID == "" {
		return nil, fmt.Errorf("oauth service %q: clientId is required", name)
	}
	if result.AuthURL == "" {
		return nil, fmt.Errorf("oauth service %q: authUrl is required", name)
	}
	if result.TokenURL == "" {
		return nil, fmt.Errorf("oauth service %q: tokenUrl is required", name)
	}

	return &result, nil
}

// --- HTTP Route Handlers ---

// HandleOAuthServices returns configured service list with connection status.
func (m *OAuthManager) HandleOAuthServices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	// List configured services.
	services := make([]map[string]any, 0)
	seen := make(map[string]bool)

	// User-configured services.
	for name, svc := range m.oauthCfg.Services {
		seen[name] = true
		entry := map[string]any{
			"name":     name,
			"authUrl":  svc.AuthURL,
			"scopes":   svc.Scopes,
			"template": false,
		}
		if _, ok := OAuthTemplates[name]; ok {
			entry["template"] = true
		}
		services = append(services, entry)
	}

	// Template-only services (not user-configured but available).
	for name := range OAuthTemplates {
		if !seen[name] {
			services = append(services, map[string]any{
				"name":     name,
				"authUrl":  OAuthTemplates[name].AuthURL,
				"template": true,
			})
		}
	}

	// Get stored token statuses.
	statuses, err := ListOAuthTokenStatuses(m.dbPath, m.encryptionKey)
	if err != nil {
		slog.Warn("list oauth token statuses", "error", err)
	}

	statusMap := make(map[string]OAuthTokenStatus)
	for _, s := range statuses {
		statusMap[s.ServiceName] = s
	}

	// Merge status into services.
	for i, svc := range services {
		name := fmt.Sprint(svc["name"])
		if st, ok := statusMap[name]; ok {
			services[i]["connected"] = st.Connected
			services[i]["expiresAt"] = st.ExpiresAt
			services[i]["expiresSoon"] = st.ExpiresSoon
			services[i]["scopes"] = st.Scopes
		} else {
			services[i]["connected"] = false
		}
	}

	json.NewEncoder(w).Encode(map[string]any{
		"services": services,
		"total":    len(services),
	})
}

// HandleOAuthRoute routes /api/oauth/{service}/{action} requests.
func (m *OAuthManager) HandleOAuthRoute(w http.ResponseWriter, r *http.Request) {
	// Parse: /api/oauth/{service}/{action}
	path := strings.TrimPrefix(r.URL.Path, "/api/oauth/")
	if path == "" || path == "/" {
		http.Error(w, `{"error":"service name required"}`, http.StatusBadRequest)
		return
	}

	parts := strings.SplitN(path, "/", 2)
	service := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	switch action {
	case "authorize":
		m.HandleAuthorize(w, r, service)
	case "callback":
		m.HandleCallback(w, r, service)
	case "revoke":
		if r.Method != http.MethodPost && r.Method != http.MethodDelete {
			http.Error(w, `{"error":"POST or DELETE only"}`, http.StatusMethodNotAllowed)
			return
		}
		if err := DeleteOAuthToken(m.dbPath, service); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"revoke: %v"}`, err), http.StatusInternalServerError)
			return
		}
		slog.Info("oauth token revoked", "service", service)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "revoked", "service": service})
	case "status":
		w.Header().Set("Content-Type", "application/json")
		token, err := LoadOAuthToken(m.dbPath, service, m.encryptionKey)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		if token == nil {
			json.NewEncoder(w).Encode(OAuthTokenStatus{ServiceName: service, Connected: false})
			return
		}
		expiresSoon := false
		if token.ExpiresAt != "" {
			if t, err := time.Parse(time.RFC3339, token.ExpiresAt); err == nil {
				expiresSoon = time.Until(t) < 5*time.Minute
			}
		}
		json.NewEncoder(w).Encode(OAuthTokenStatus{
			ServiceName: service,
			Connected:   true,
			Scopes:      token.Scopes,
			ExpiresAt:   token.ExpiresAt,
			ExpiresSoon: expiresSoon,
			CreatedAt:   token.CreatedAt,
		})
	default:
		http.Error(w, fmt.Sprintf(`{"error":"unknown action %q, use: authorize, callback, revoke, status"}`, action), http.StatusBadRequest)
	}
}
