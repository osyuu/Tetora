package discord

import (
	"crypto/ed25519"
	"encoding/hex"
	"time"

	"tetora/internal/log"
)

// --- Component Builders ---

// ActionRow creates an action row containing components.
func ActionRow(components ...Component) Component {
	return Component{
		Type:       ComponentTypeActionRow,
		Components: components,
	}
}

// Button creates a button component.
func Button(customID, label string, style int) Component {
	c := Component{
		Type:     ComponentTypeButton,
		CustomID: customID,
		Label:    label,
		Style:    style,
	}
	// Link buttons don't use custom_id, they use url.
	if style == ButtonStyleLink {
		c.URL = customID
		c.CustomID = ""
	}
	return c
}

// LinkButton creates a link button with a URL.
func LinkButton(url, label string) Component {
	return Component{
		Type:  ComponentTypeButton,
		Label: label,
		Style: ButtonStyleLink,
		URL:   url,
	}
}

// SelectMenu creates a string select menu.
func SelectMenu(customID, placeholder string, options []SelectOption) Component {
	return Component{
		Type:        ComponentTypeStringSelect,
		CustomID:    customID,
		Placeholder: placeholder,
		Options:     options,
	}
}

// MultiSelectMenu creates a string select menu with multi-select enabled.
func MultiSelectMenu(customID, placeholder string, options []SelectOption, maxValues int) Component {
	minV := 0
	maxV := maxValues
	return Component{
		Type:        ComponentTypeStringSelect,
		CustomID:    customID,
		Placeholder: placeholder,
		Options:     options,
		MinValues:   &minV,
		MaxValues:   &maxV,
	}
}

// UserSelect creates a user select menu.
func UserSelect(customID, placeholder string) Component {
	return Component{
		Type:        ComponentTypeUserSelect,
		CustomID:    customID,
		Placeholder: placeholder,
	}
}

// RoleSelect creates a role select menu.
func RoleSelect(customID, placeholder string) Component {
	return Component{
		Type:        ComponentTypeRoleSelect,
		CustomID:    customID,
		Placeholder: placeholder,
	}
}

// ChannelSelect creates a channel select menu.
func ChannelSelect(customID, placeholder string) Component {
	return Component{
		Type:        ComponentTypeChannelSelect,
		CustomID:    customID,
		Placeholder: placeholder,
	}
}

// TextInput creates a text input for use in modals.
func TextInput(customID, label string, required bool) Component {
	return Component{
		Type:     ComponentTypeTextInput,
		CustomID: customID,
		Label:    label,
		Style:    TextInputStyleShort,
		Required: required,
	}
}

// ParagraphInput creates a paragraph (multi-line) text input for modals.
func ParagraphInput(customID, label string, required bool) Component {
	return Component{
		Type:     ComponentTypeTextInput,
		CustomID: customID,
		Label:    label,
		Style:    TextInputStyleParagraph,
		Required: required,
	}
}

// BuildModal creates a modal interaction response.
func BuildModal(customID, title string, components ...Component) InteractionResponse {
	// Wrap text inputs in action rows if they aren't already.
	rows := make([]Component, 0, len(components))
	for _, c := range components {
		if c.Type == ComponentTypeActionRow {
			rows = append(rows, c)
		} else {
			rows = append(rows, ActionRow(c))
		}
	}
	return InteractionResponse{
		Type: InteractionResponseModal,
		Data: &InteractionResponseData{
			CustomID:   customID,
			Title:      title,
			Components: rows,
		},
	}
}

// ApprovalButtons creates approve/reject buttons for a task.
func ApprovalButtons(taskID string) []Component {
	return []Component{
		ActionRow(
			Button("approve:"+taskID, "Approve", ButtonStyleSuccess),
			Button("reject:"+taskID, "Reject", ButtonStyleDanger),
		),
	}
}

// AgentSelectMenu creates a select menu for choosing an agent.
func AgentSelectMenu(agents []string) []Component {
	options := make([]SelectOption, len(agents))
	for i, a := range agents {
		options[i] = SelectOption{Label: a, Value: a}
	}
	return []Component{
		ActionRow(
			SelectMenu("agent_select", "Select an agent...", options),
		),
	}
}

// --- Ed25519 Signature Verification ---

// VerifySignature verifies a Discord interaction webhook signature.
// Discord sends X-Signature-Ed25519 (hex-encoded signature) and X-Signature-Timestamp headers.
// The signed message is timestamp + body.
func VerifySignature(publicKeyHex, signature, timestamp string, body []byte) bool {
	pubKeyBytes, err := hex.DecodeString(publicKeyHex)
	if err != nil || len(pubKeyBytes) != ed25519.PublicKeySize {
		return false
	}

	sigBytes, err := hex.DecodeString(signature)
	if err != nil || len(sigBytes) != ed25519.SignatureSize {
		return false
	}

	msg := []byte(timestamp + string(body))
	return ed25519.Verify(ed25519.PublicKey(pubKeyBytes), msg, sigBytes)
}

// --- Interaction Helpers ---

// InteractionUserID extracts the user ID from an interaction (guild or DM).
func InteractionUserID(i *Interaction) string {
	if i.Member != nil {
		return i.Member.User.ID
	}
	if i.User != nil {
		return i.User.ID
	}
	return ""
}

// ExtractModalValues extracts field values from modal submit components.
// Modal components are action rows containing text inputs.
func ExtractModalValues(components []Component) map[string]string {
	values := make(map[string]string)
	for _, row := range components {
		if row.Type == ComponentTypeActionRow {
			for _, field := range row.Components {
				if field.CustomID != "" {
					values[field.CustomID] = field.Value
				}
			}
		}
	}
	return values
}

// ContainsStr checks if a string slice contains a value.
func ContainsStr(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}

// --- Callback Helpers ---

// RunCallbackWithTimeout runs a Discord interaction callback with a 30-second timeout guard.
// The callback itself is not cancelled — this only logs if it exceeds the timeout.
func RunCallbackWithTimeout(cb func(InteractionData), data InteractionData) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		cb(data)
	}()
	go func() {
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			log.Warn("discord callback exceeded 30s timeout", "customID", data.CustomID)
		}
	}()
}
