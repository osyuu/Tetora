// Package pairing manages user pairing/approval for messaging channel access.
package pairing

import (
	"crypto/rand"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"tetora/internal/db"
	"tetora/internal/log"
)

// Config holds the pairing-relevant config fields.
type Config struct {
	HistoryDB      string
	DMPairing      bool
	PairingExpiry  string
	PairingMessage string
	Allowlists     map[string][]string // channel → allowed user IDs
}

// Manager manages in-memory pending pairing requests and DB-backed approvals.
type Manager struct {
	mu      sync.RWMutex
	cfg     Config
	pending map[string]*Request // code → request
}

// Request represents a pending pairing request.
type Request struct {
	Code      string    `json:"code"`
	Channel   string    `json:"channel"` // "telegram", "slack", "discord", "whatsapp"
	UserID    string    `json:"userId"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// New creates a new Manager with the given config.
func New(cfg Config) *Manager {
	pm := &Manager{
		cfg:     cfg,
		pending: make(map[string]*Request),
	}
	if err := pm.initDB(); err != nil {
		log.Warn("init pairing db failed", "error", err)
	}
	go pm.cleanupExpired()
	return pm
}

func (pm *Manager) cleanupExpired() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		pm.mu.Lock()
		now := time.Now()
		for code, req := range pm.pending {
			if now.After(req.ExpiresAt) {
				delete(pm.pending, code)
			}
		}
		pm.mu.Unlock()
	}
}

// IsAllowed checks if a user is allowed to interact with the bot.
func (pm *Manager) IsAllowed(channel, userID string) bool {
	if !pm.cfg.DMPairing {
		return true
	}
	if allowlist, ok := pm.cfg.Allowlists[channel]; ok {
		for _, id := range allowlist {
			if id == userID {
				return true
			}
		}
	}
	return pm.isApprovedInDB(channel, userID)
}

// RequestPairing generates a 6-digit code and stores the pending request.
func (pm *Manager) RequestPairing(channel, userID, username string) (string, error) {
	code := pm.generateCode()

	expiryStr := pm.cfg.PairingExpiry
	if expiryStr == "" {
		expiryStr = "15m"
	}
	expiry, err := time.ParseDuration(expiryStr)
	if err != nil {
		expiry = 15 * time.Minute
	}

	now := time.Now()
	req := &Request{
		Code:      code,
		Channel:   channel,
		UserID:    userID,
		Username:  username,
		CreatedAt: now,
		ExpiresAt: now.Add(expiry),
	}

	pm.mu.Lock()
	pm.pending[code] = req
	pm.mu.Unlock()

	msg := pm.cfg.PairingMessage
	if msg == "" {
		msg = "Please approve this request using the code: {{.Code}}"
	}
	msg = strings.ReplaceAll(msg, "{{.Code}}", code)
	return msg, nil
}

// Approve finds a pending request by code, stores approval in DB.
func (pm *Manager) Approve(code string) (*Request, error) {
	pm.mu.Lock()
	req, ok := pm.pending[code]
	if !ok {
		pm.mu.Unlock()
		return nil, fmt.Errorf("invalid or expired code")
	}
	if time.Now().After(req.ExpiresAt) {
		delete(pm.pending, code)
		pm.mu.Unlock()
		return nil, fmt.Errorf("code expired")
	}
	delete(pm.pending, code)
	pm.mu.Unlock()

	if err := pm.storeApproval(req.Channel, req.UserID, req.Username); err != nil {
		return nil, fmt.Errorf("store approval failed: %w", err)
	}
	return req, nil
}

// Reject removes a pending request by code.
func (pm *Manager) Reject(code string) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if _, ok := pm.pending[code]; !ok {
		return fmt.Errorf("invalid or expired code")
	}
	delete(pm.pending, code)
	return nil
}

// ListPending returns all pending pairing requests.
func (pm *Manager) ListPending() []*Request {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	var result []*Request
	for _, req := range pm.pending {
		result = append(result, req)
	}
	return result
}

// ListApproved returns all approved users from DB.
func (pm *Manager) ListApproved() ([]map[string]any, error) {
	sql := `SELECT channel, user_id, username, approved_at FROM pairing_approved ORDER BY approved_at DESC`
	return db.Query(pm.cfg.HistoryDB, sql)
}

// Revoke removes an approved user from DB.
func (pm *Manager) Revoke(channel, userID string) error {
	return pm.removeApproval(channel, userID)
}

// --- DB Storage ---

func (pm *Manager) initDB() error {
	sql := `CREATE TABLE IF NOT EXISTS pairing_approved (
		channel TEXT NOT NULL,
		user_id TEXT NOT NULL,
		username TEXT NOT NULL,
		approved_at TEXT NOT NULL,
		PRIMARY KEY(channel, user_id)
	)`
	cmd := exec.Command("sqlite3", pm.cfg.HistoryDB, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sqlite3: %s: %w", string(out), err)
	}
	return nil
}

func (pm *Manager) isApprovedInDB(channel, userID string) bool {
	sql := fmt.Sprintf(
		`SELECT COUNT(*) as cnt FROM pairing_approved WHERE channel = '%s' AND user_id = '%s'`,
		db.Escape(channel), db.Escape(userID))
	rows, err := db.Query(pm.cfg.HistoryDB, sql)
	if err != nil || len(rows) == 0 {
		return false
	}
	cnt, ok := rows[0]["cnt"].(float64)
	return ok && cnt > 0
}

func (pm *Manager) storeApproval(channel, userID, username string) error {
	sql := fmt.Sprintf(
		`INSERT OR REPLACE INTO pairing_approved (channel, user_id, username, approved_at)
		 VALUES ('%s', '%s', '%s', datetime('now'))`,
		db.Escape(channel), db.Escape(userID), db.Escape(username))
	cmd := exec.Command("sqlite3", pm.cfg.HistoryDB, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sqlite3: %s: %w", string(out), err)
	}
	return nil
}

func (pm *Manager) removeApproval(channel, userID string) error {
	sql := fmt.Sprintf(
		`DELETE FROM pairing_approved WHERE channel = '%s' AND user_id = '%s'`,
		db.Escape(channel), db.Escape(userID))
	cmd := exec.Command("sqlite3", pm.cfg.HistoryDB, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sqlite3: %s: %w", string(out), err)
	}
	return nil
}

func (pm *Manager) generateCode() string {
	for {
		b := make([]byte, 3)
		if _, err := rand.Read(b); err != nil {
			return fmt.Sprintf("%06d", time.Now().Unix()%1000000)
		}
		num := int(b[0])<<16 | int(b[1])<<8 | int(b[2])
		code := num % 1000000
		codeStr := fmt.Sprintf("%06d", code)
		pm.mu.RLock()
		_, exists := pm.pending[codeStr]
		pm.mu.RUnlock()
		if !exists {
			return codeStr
		}
	}
}
