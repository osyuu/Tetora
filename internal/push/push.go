package push

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"tetora/internal/db"
	"tetora/internal/log"
)

type Subscription struct {
	Endpoint  string          `json:"endpoint"`
	Keys      SubscriptionKeys `json:"keys"`
	CreatedAt string          `json:"createdAt,omitempty"`
	UserAgent string          `json:"userAgent,omitempty"`
}

type SubscriptionKeys struct {
	P256dh string `json:"p256dh"`
	Auth   string `json:"auth"`
}

type Notification struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Icon  string `json:"icon,omitempty"`
	Tag   string `json:"tag,omitempty"`
	URL   string `json:"url,omitempty"`
}

type Config struct {
	HistoryDB       string
	VAPIDPrivateKey string
	VAPIDEmail      string
	TTL             int
}

type Manager struct {
	cfg           Config
	subscriptions map[string]Subscription
	mu            sync.RWMutex
	dbPath        string
}

func NewManager(cfg Config) *Manager {
	m := &Manager{
		cfg:           cfg,
		subscriptions: make(map[string]Subscription),
		dbPath:        cfg.HistoryDB,
	}
	m.initDB()
	m.loadFromDB()
	return m
}

func (m *Manager) initDB() {
	sql := `CREATE TABLE IF NOT EXISTS push_subscriptions (
		endpoint TEXT PRIMARY KEY,
		p256dh TEXT NOT NULL,
		auth TEXT NOT NULL,
		user_agent TEXT DEFAULT '',
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	);`
	if _, err := db.Query(m.dbPath, sql); err != nil {
		log.Warn("push: init db failed", "error", err)
	}
}

func (m *Manager) loadFromDB() {
	rows, err := db.Query(m.dbPath, "SELECT endpoint, p256dh, auth, user_agent, created_at FROM push_subscriptions")
	if err != nil {
		log.Warn("push: load from db failed", "error", err)
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, row := range rows {
		sub := Subscription{
			Endpoint: row["endpoint"].(string),
			Keys: SubscriptionKeys{
				P256dh: row["p256dh"].(string),
				Auth:   row["auth"].(string),
			},
			UserAgent: row["user_agent"].(string),
			CreatedAt: row["created_at"].(string),
		}
		m.subscriptions[sub.Endpoint] = sub
	}
	log.Info("push: loaded subscriptions", "count", len(m.subscriptions))
}

func (m *Manager) Subscribe(sub Subscription) error {
	if sub.Endpoint == "" || sub.Keys.P256dh == "" || sub.Keys.Auth == "" {
		return errors.New("invalid subscription: missing required fields")
	}

	u, err := url.Parse(sub.Endpoint)
	if err != nil {
		return fmt.Errorf("invalid endpoint URL: %w", err)
	}
	if !u.IsAbs() || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("invalid endpoint: must be absolute http/https URL")
	}

	if sub.CreatedAt == "" {
		sub.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}

	m.mu.Lock()
	m.subscriptions[sub.Endpoint] = sub
	m.mu.Unlock()

	sql := fmt.Sprintf(
		`INSERT OR REPLACE INTO push_subscriptions (endpoint, p256dh, auth, user_agent, created_at) VALUES ('%s', '%s', '%s', '%s', '%s')`,
		db.Escape(sub.Endpoint),
		db.Escape(sub.Keys.P256dh),
		db.Escape(sub.Keys.Auth),
		db.Escape(sub.UserAgent),
		db.Escape(sub.CreatedAt),
	)
	if _, err := db.Query(m.dbPath, sql); err != nil {
		log.Warn("push: save subscription failed", "error", err)
		return err
	}

	log.Info("push: subscription saved", "endpoint", sub.Endpoint)
	return nil
}

func (m *Manager) Unsubscribe(endpoint string) error {
	m.mu.Lock()
	delete(m.subscriptions, endpoint)
	m.mu.Unlock()

	sql := fmt.Sprintf(`DELETE FROM push_subscriptions WHERE endpoint = '%s'`, db.Escape(endpoint))
	if _, err := db.Query(m.dbPath, sql); err != nil {
		log.Warn("push: unsubscribe failed", "error", err)
		return err
	}

	log.Info("push: subscription removed", "endpoint", endpoint)
	return nil
}

func (m *Manager) SendNotification(notif Notification) error {
	m.mu.RLock()
	subs := make([]Subscription, 0, len(m.subscriptions))
	for _, sub := range m.subscriptions {
		subs = append(subs, sub)
	}
	m.mu.RUnlock()

	if len(subs) == 0 {
		return errors.New("no subscriptions")
	}

	var errs []string
	for _, sub := range subs {
		if err := m.SendToEndpoint(sub.Endpoint, notif); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", sub.Endpoint, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to send to %d/%d subscribers: %s", len(errs), len(subs), strings.Join(errs, "; "))
	}

	log.Info("push: notification sent to all subscribers", "count", len(subs))
	return nil
}

func (m *Manager) SendToEndpoint(endpoint string, notif Notification) error {
	m.mu.RLock()
	sub, ok := m.subscriptions[endpoint]
	m.mu.RUnlock()

	if !ok {
		return errors.New("subscription not found")
	}

	payload, err := json.Marshal(notif)
	if err != nil {
		return fmt.Errorf("marshal notification: %w", err)
	}

	encrypted, encHeader, cryptoHeader, err := EncryptPayload(payload, sub.Keys.P256dh, sub.Keys.Auth)
	if err != nil {
		return fmt.Errorf("encrypt payload: %w", err)
	}

	authHeader, err := GenerateVAPIDAuth(endpoint, m.cfg.VAPIDPrivateKey, m.cfg.VAPIDEmail)
	if err != nil {
		return fmt.Errorf("generate VAPID auth: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(string(encrypted)))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	ttl := m.cfg.TTL
	if ttl <= 0 {
		ttl = 3600
	}

	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Encryption", encHeader)
	req.Header.Set("Crypto-Key", cryptoHeader)
	req.Header.Set("Authorization", authHeader)
	req.Header.Set("TTL", fmt.Sprintf("%d", ttl))

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send push request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == 410 {
			log.Info("push: subscription expired (410), removing", "endpoint", endpoint)
			m.Unsubscribe(endpoint)
		}
		return fmt.Errorf("push server returned %d: %s", resp.StatusCode, string(body))
	}

	log.Info("push: notification sent", "endpoint", endpoint, "status", resp.StatusCode)
	return nil
}

func (m *Manager) ListSubscriptions() []Subscription {
	m.mu.RLock()
	defer m.mu.RUnlock()

	subs := make([]Subscription, 0, len(m.subscriptions))
	for _, sub := range m.subscriptions {
		subs = append(subs, sub)
	}
	return subs
}

func GenerateVAPIDAuth(endpoint, vapidPrivateKey, vapidEmail string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("parse endpoint: %w", err)
	}
	origin := fmt.Sprintf("%s://%s", u.Scheme, u.Host)

	privKeyBytes, err := base64.RawURLEncoding.DecodeString(vapidPrivateKey)
	if err != nil {
		return "", fmt.Errorf("decode private key: %w", err)
	}
	if len(privKeyBytes) != 32 {
		return "", errors.New("invalid private key length (expected 32 bytes)")
	}

	curve := elliptic.P256()
	d := new(big.Int).SetBytes(privKeyBytes)
	x, y := curve.ScalarBaseMult(privKeyBytes)
	privKey := &ecdsa.PrivateKey{
		PublicKey: ecdsa.PublicKey{
			Curve: curve,
			X:     x,
			Y:     y,
		},
		D: d,
	}

	pubKeyBytes := elliptic.Marshal(curve, privKey.PublicKey.X, privKey.PublicKey.Y)
	vapidPublicKey := base64.RawURLEncoding.EncodeToString(pubKeyBytes)

	now := time.Now().Unix()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(
		`{"aud":%q,"exp":%d,"sub":"mailto:%s"}`,
		origin,
		now+43200,
		vapidEmail,
	)))

	message := header + "." + claims
	hash := sha256.Sum256([]byte(message))
	r, s, err := ecdsa.Sign(rand.Reader, privKey, hash[:])
	if err != nil {
		return "", fmt.Errorf("sign JWT: %w", err)
	}

	sig := append(r.Bytes(), s.Bytes()...)
	if len(sig) < 64 {
		padded := make([]byte, 64)
		copy(padded[32-len(r.Bytes()):32], r.Bytes())
		copy(padded[64-len(s.Bytes()):], s.Bytes())
		sig = padded
	}
	signature := base64.RawURLEncoding.EncodeToString(sig)

	jwt := message + "." + signature

	return fmt.Sprintf("vapid t=%s,k=%s", jwt, vapidPublicKey), nil
}

func EncryptPayload(payload []byte, p256dh, auth string) ([]byte, string, string, error) {
	subPubKey, err := base64.RawURLEncoding.DecodeString(p256dh)
	if err != nil {
		return nil, "", "", fmt.Errorf("decode p256dh: %w", err)
	}
	authSecret, err := base64.RawURLEncoding.DecodeString(auth)
	if err != nil {
		return nil, "", "", fmt.Errorf("decode auth: %w", err)
	}

	curve := elliptic.P256()
	localPrivKey, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", "", fmt.Errorf("generate ephemeral key: %w", err)
	}
	localPubKeyBytes := localPrivKey.PublicKey().Bytes()

	subX, _ := elliptic.Unmarshal(curve, subPubKey)
	if subX == nil {
		return nil, "", "", errors.New("invalid subscriber public key")
	}
	subPubKeyECDH, err := ecdh.P256().NewPublicKey(subPubKey)
	if err != nil {
		return nil, "", "", fmt.Errorf("parse subscriber public key: %w", err)
	}

	sharedSecret, err := localPrivKey.ECDH(subPubKeyECDH)
	if err != nil {
		return nil, "", "", fmt.Errorf("ECDH: %w", err)
	}

	prk := hmacSHA256(authSecret, sharedSecret)

	ikmInfo := append([]byte("Content-Encoding: auth\x00"), subPubKey...)
	ikmInfo = append(ikmInfo, localPubKeyBytes...)
	ikm := hkdfExpand(prk, ikmInfo, 32)

	salt := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, "", "", fmt.Errorf("generate salt: %w", err)
	}

	prkCEK := hmacSHA256(salt, ikm)
	cek := hkdfExpand(prkCEK, []byte("Content-Encoding: aes128gcm\x00"), 16)
	nonce := hkdfExpand(prkCEK, []byte("Content-Encoding: nonce\x00"), 12)

	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, "", "", fmt.Errorf("create AES cipher: %w", err)
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, "", "", fmt.Errorf("create GCM: %w", err)
	}

	record := append(payload, 0x02)
	ciphertext := aesgcm.Seal(nil, nonce, record, nil)

	rs := uint32(4096)
	encrypted := make([]byte, 0, 16+4+1+len(ciphertext))
	encrypted = append(encrypted, salt...)
	rsBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(rsBytes, rs)
	encrypted = append(encrypted, rsBytes...)
	encrypted = append(encrypted, 0x00)
	encrypted = append(encrypted, ciphertext...)

	encHeader := fmt.Sprintf("salt=%s", base64.RawURLEncoding.EncodeToString(salt))
	cryptoHeader := fmt.Sprintf("dh=%s", base64.RawURLEncoding.EncodeToString(localPubKeyBytes))

	return encrypted, encHeader, cryptoHeader, nil
}

func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

func hkdfExpand(prk, info []byte, length int) []byte {
	hashLen := sha256.Size
	n := (length + hashLen - 1) / hashLen
	okm := make([]byte, 0, n*hashLen)
	prev := []byte{}
	for i := 1; i <= n; i++ {
		mac := hmac.New(sha256.New, prk)
		mac.Write(prev)
		mac.Write(info)
		mac.Write([]byte{byte(i)})
		block := mac.Sum(nil)
		okm = append(okm, block...)
		prev = block
	}
	return okm[:length]
}
