package main

import (
	"context"
	"encoding/json"
	"fmt"
)

// --- Family Mode ---
// Service struct, types, and method implementations are in internal/life/family/.
// This file keeps config type, tool handlers, and the global singleton.

// FamilyConfig holds settings for multi-user / family mode.
type FamilyConfig struct {
	Enabled          bool    `json:"enabled"`
	MaxUsers         int     `json:"maxUsers,omitempty"`         // default 10
	DefaultBudget    float64 `json:"defaultBudget,omitempty"`    // monthly USD, 0=unlimited
	DefaultRateLimit int     `json:"defaultRateLimit,omitempty"` // daily requests, default 100
}

func (c FamilyConfig) maxUsersOrDefault() int {
	if c.MaxUsers > 0 {
		return c.MaxUsers
	}
	return 10
}

func (c FamilyConfig) defaultRateLimitOrDefault() int {
	if c.DefaultRateLimit > 0 {
		return c.DefaultRateLimit
	}
	return 100
}

// globalFamilyService is the singleton family service.
var globalFamilyService *FamilyService

// --- Tool Handlers ---

// toolFamilyListAdd adds an item to a shared list.
func toolFamilyListAdd(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalFamilyService == nil {
		return "", fmt.Errorf("family mode not enabled")
	}

	var args struct {
		ListID   string `json:"listId"`
		Text     string `json:"text"`
		Quantity string `json:"quantity"`
		AddedBy  string `json:"addedBy"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Text == "" {
		return "", fmt.Errorf("text is required")
	}
	if args.AddedBy == "" {
		args.AddedBy = "default"
	}

	// If listId not provided, use the first shopping list or create one.
	if args.ListID == "" {
		lists, err := globalFamilyService.ListLists()
		if err != nil {
			return "", err
		}
		for _, l := range lists {
			if l.ListType == "shopping" {
				args.ListID = l.ID
				break
			}
		}
		if args.ListID == "" {
			list, err := globalFamilyService.CreateList("Shopping", "shopping", args.AddedBy, newUUID)
			if err != nil {
				return "", fmt.Errorf("create default shopping list: %w", err)
			}
			args.ListID = list.ID
		}
	}

	item, err := globalFamilyService.AddListItem(args.ListID, args.Text, args.Quantity, args.AddedBy)
	if err != nil {
		return "", err
	}

	b, _ := json.Marshal(map[string]any{
		"status": "added",
		"item":   item,
	})
	return string(b), nil
}

// toolFamilyListView lists shared lists or items.
func toolFamilyListView(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalFamilyService == nil {
		return "", fmt.Errorf("family mode not enabled")
	}

	var args struct {
		ListID   string `json:"listId"`
		ListType string `json:"listType"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	// If listId provided, show items for that list.
	if args.ListID != "" {
		items, err := globalFamilyService.GetListItems(args.ListID)
		if err != nil {
			return "", err
		}
		list, _ := globalFamilyService.GetList(args.ListID)
		result := map[string]any{
			"items": items,
		}
		if list != nil {
			result["list"] = list
		}
		b, _ := json.Marshal(result)
		return string(b), nil
	}

	// Otherwise, show all lists (optionally filtered by type).
	lists, err := globalFamilyService.ListLists()
	if err != nil {
		return "", err
	}
	if args.ListType != "" {
		var filtered []SharedList
		for _, l := range lists {
			if l.ListType == args.ListType {
				filtered = append(filtered, l)
			}
		}
		lists = filtered
	}

	b, _ := json.Marshal(map[string]any{"lists": lists})
	return string(b), nil
}

// toolUserSwitch switches the active user context.
func toolUserSwitch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalFamilyService == nil {
		return "", fmt.Errorf("family mode not enabled")
	}

	var args struct {
		UserID string `json:"userId"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.UserID == "" {
		return "", fmt.Errorf("userId is required")
	}

	user, err := globalFamilyService.GetUser(args.UserID)
	if err != nil {
		return "", fmt.Errorf("user not found or inactive: %w", err)
	}

	// Check rate limit.
	allowed, remaining, _ := globalFamilyService.CheckRateLimit(args.UserID)

	perms, _ := globalFamilyService.GetPermissions(args.UserID)

	b, _ := json.Marshal(map[string]any{
		"status":      "switched",
		"user":        user,
		"permissions": perms,
		"rateLimit": map[string]any{
			"allowed":   allowed,
			"remaining": remaining,
		},
	})
	return string(b), nil
}

// toolFamilyManage is a multipurpose family management tool.
func toolFamilyManage(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalFamilyService == nil {
		return "", fmt.Errorf("family mode not enabled")
	}

	var args struct {
		Action      string  `json:"action"`
		UserID      string  `json:"userId"`
		DisplayName string  `json:"displayName"`
		Role        string  `json:"role"`
		Permission  string  `json:"permission"`
		Grant       bool    `json:"grant"`
		RateLimit   int     `json:"rateLimit"`
		Budget      float64 `json:"budget"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	switch args.Action {
	case "add":
		if args.Role == "" {
			args.Role = "member"
		}
		if err := globalFamilyService.AddUser(args.UserID, args.DisplayName, args.Role); err != nil {
			return "", err
		}
		user, _ := globalFamilyService.GetUser(args.UserID)
		b, _ := json.Marshal(map[string]any{"status": "added", "user": user})
		return string(b), nil

	case "remove":
		if err := globalFamilyService.RemoveUser(args.UserID); err != nil {
			return "", err
		}
		b, _ := json.Marshal(map[string]any{"status": "removed", "userId": args.UserID})
		return string(b), nil

	case "list":
		users, err := globalFamilyService.ListUsers()
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(map[string]any{"users": users})
		return string(b), nil

	case "update":
		updates := make(map[string]any)
		if args.DisplayName != "" {
			updates["displayName"] = args.DisplayName
		}
		if args.Role != "" {
			updates["role"] = args.Role
		}
		if args.RateLimit > 0 {
			updates["rateLimitDaily"] = float64(args.RateLimit)
		}
		if args.Budget > 0 {
			updates["budgetMonthly"] = args.Budget
		}
		if err := globalFamilyService.UpdateUser(args.UserID, updates); err != nil {
			return "", err
		}
		user, _ := globalFamilyService.GetUser(args.UserID)
		b, _ := json.Marshal(map[string]any{"status": "updated", "user": user})
		return string(b), nil

	case "permissions":
		if args.Permission != "" {
			if args.Grant {
				if err := globalFamilyService.GrantPermission(args.UserID, args.Permission); err != nil {
					return "", err
				}
			} else {
				if err := globalFamilyService.RevokePermission(args.UserID, args.Permission); err != nil {
					return "", err
				}
			}
		}
		perms, err := globalFamilyService.GetPermissions(args.UserID)
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(map[string]any{"userId": args.UserID, "permissions": perms})
		return string(b), nil

	default:
		return "", fmt.Errorf("unknown action: %s (use add, remove, list, update, or permissions)", args.Action)
	}
}
