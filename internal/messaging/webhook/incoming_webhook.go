// Package webhook implements incoming webhook processing logic.
package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// Config defines an incoming webhook that triggers agent execution.
type Config struct {
	Agent    string `json:"agent"`             // target agent for dispatch
	Template string `json:"template,omitempty"` // prompt template with {{payload.xxx}} placeholders
	Secret   string `json:"secret,omitempty"`   // HMAC-SHA256 signature verification
	Filter   string `json:"filter,omitempty"`   // simple condition: "payload.action == 'opened'"
	Workflow string `json:"workflow,omitempty"` // workflow name to trigger instead of dispatch
	Enabled  *bool  `json:"enabled,omitempty"`  // default true
}

// IsEnabled returns whether the webhook is enabled (default true).
func (c Config) IsEnabled() bool {
	if c.Enabled == nil {
		return true
	}
	return *c.Enabled
}

// VerifySignature checks the request signature against the shared secret.
// Supports GitHub (X-Hub-Signature-256), GitLab (X-Gitlab-Token), and generic (X-Webhook-Signature).
func VerifySignature(r *http.Request, body []byte, secret string) bool {
	if secret == "" {
		return true // no secret = skip verification
	}

	// GitHub: X-Hub-Signature-256 = sha256=<hex>
	if sig := r.Header.Get("X-Hub-Signature-256"); sig != "" {
		return VerifyHMACSHA256(body, secret, strings.TrimPrefix(sig, "sha256="))
	}

	// GitLab: X-Gitlab-Token = <secret>
	if token := r.Header.Get("X-Gitlab-Token"); token != "" {
		return hmac.Equal([]byte(token), []byte(secret))
	}

	// Generic: X-Webhook-Signature = <hex hmac-sha256>
	if sig := r.Header.Get("X-Webhook-Signature"); sig != "" {
		return VerifyHMACSHA256(body, secret, sig)
	}

	// No signature header found — reject if secret is configured.
	return false
}

// VerifyHMACSHA256 checks HMAC-SHA256 signature.
func VerifyHMACSHA256(body []byte, secret, signatureHex string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signatureHex))
}

// ExpandTemplate replaces {{payload.xxx}} and {{payload.xxx.yyy}} placeholders
// with values from the parsed JSON payload.
func ExpandTemplate(template string, payload map[string]any) string {
	re := regexp.MustCompile(`\{\{payload\.([a-zA-Z0-9_.]+)\}\}`)
	return re.ReplaceAllStringFunc(template, func(match string) string {
		// Extract the path: "payload.pull_request.title" -> "pull_request.title"
		path := match[10 : len(match)-2] // strip "{{payload." and "}}"
		val := GetNestedValue(payload, path)
		if val == nil {
			return match // keep original if not found
		}
		switch v := val.(type) {
		case string:
			return v
		case float64:
			if v == float64(int(v)) {
				return fmt.Sprintf("%d", int(v))
			}
			return fmt.Sprintf("%g", v)
		case bool:
			return fmt.Sprintf("%v", v)
		default:
			b, _ := json.Marshal(v)
			return string(b)
		}
	})
}

// GetNestedValue retrieves a value from a nested map using dot notation.
func GetNestedValue(m map[string]any, path string) any {
	parts := strings.Split(path, ".")
	var current any = m
	for _, part := range parts {
		cm, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current, ok = cm[part]
		if !ok {
			return nil
		}
	}
	return current
}

// EvaluateFilter checks if a payload matches a simple filter expression.
// Supported: "payload.key == 'value'", "payload.key != 'value'", "payload.key" (truthy check).
func EvaluateFilter(filter string, payload map[string]any) bool {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return true // no filter = accept all
	}

	// "payload.key == 'value'" or "payload.key != 'value'"
	for _, op := range []string{"==", "!="} {
		if parts := strings.SplitN(filter, op, 2); len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			val = strings.Trim(val, "'\"")

			// Strip "payload." prefix.
			key = strings.TrimPrefix(key, "payload.")

			actual := GetNestedValue(payload, key)
			actualStr := fmt.Sprintf("%v", actual)

			if op == "==" {
				return actualStr == val
			}
			return actualStr != val
		}
	}

	// Truthy check: "payload.key"
	key := strings.TrimPrefix(filter, "payload.")
	val := GetNestedValue(payload, key)
	return IsTruthy(val)
}

// IsTruthy returns whether a value is considered truthy.
func IsTruthy(val any) bool {
	if val == nil {
		return false
	}
	switch v := val.(type) {
	case bool:
		return v
	case string:
		return v != ""
	case float64:
		return v != 0
	default:
		return true
	}
}
