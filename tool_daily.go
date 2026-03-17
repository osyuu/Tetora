package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"tetora/internal/log"
)

// --- Lesson Tool Handler ---

func toolStoreLesson(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Category string   `json:"category"`
		Lesson   string   `json:"lesson"`
		Source   string   `json:"source"`
		Tags     []string `json:"tags"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Category == "" {
		return "", fmt.Errorf("category is required")
	}
	if args.Lesson == "" {
		return "", fmt.Errorf("lesson is required")
	}

	category := sanitizeLessonCategory(args.Category)
	noteName := "lessons/" + category

	svc := getGlobalNotesService()
	if svc == nil {
		return "", fmt.Errorf("notes service is not enabled")
	}

	now := time.Now().Format("2006-01-02 15:04")
	var entry strings.Builder
	entry.WriteString(fmt.Sprintf("\n## %s\n", now))
	entry.WriteString(fmt.Sprintf("- %s\n", args.Lesson))
	if args.Source != "" {
		entry.WriteString(fmt.Sprintf("- Source: %s\n", args.Source))
	}
	if len(args.Tags) > 0 {
		entry.WriteString(fmt.Sprintf("- Tags: %s\n", strings.Join(args.Tags, ", ")))
	}

	if err := svc.AppendNote(noteName, entry.String()); err != nil {
		return "", fmt.Errorf("append to vault: %w", err)
	}

	lessonsFile := "tasks/lessons.md"
	if _, err := os.Stat(lessonsFile); err == nil {
		sectionHeader := "## " + args.Category
		line := fmt.Sprintf("- %s", args.Lesson)
		if err := appendToLessonSection(lessonsFile, sectionHeader, line); err != nil {
			log.Warn("append to lessons.md failed", "error", err)
		}
	}

	if cfg.HistoryDB != "" {
		recordSkillEvent(cfg.HistoryDB, category, "lesson", args.Lesson, args.Source)
	}

	log.InfoCtx(ctx, "lesson stored", "category", category, "tags", args.Tags)

	result := map[string]any{
		"status":   "stored",
		"category": category,
		"vault":    noteName,
	}
	b, _ := json.Marshal(result)
	return string(b), nil
}

func sanitizeLessonCategory(cat string) string {
	cat = strings.ToLower(strings.TrimSpace(cat))
	re := regexp.MustCompile(`[^a-z0-9-]+`)
	cat = re.ReplaceAllString(cat, "-")
	cat = strings.Trim(cat, "-")
	if cat == "" {
		cat = "general"
	}
	return cat
}

func appendToLessonSection(filePath, sectionHeader, content string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	lines := strings.Split(string(data), "\n")
	var result []string
	inserted := false

	for i, line := range lines {
		result = append(result, line)
		if strings.TrimSpace(line) == sectionHeader {
			j := i + 1
			for j < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[j]), "## ") {
				j++
			}
			insertIdx := j
			for insertIdx > i+1 && strings.TrimSpace(lines[insertIdx-1]) == "" {
				insertIdx--
			}
			for k := i + 1; k < insertIdx; k++ {
				result = append(result, lines[k])
			}
			result = append(result, content)
			for k := insertIdx; k < len(lines); k++ {
				result = append(result, lines[k])
			}
			inserted = true
			break
		}
	}

	if !inserted {
		result = append(result, "", sectionHeader, content)
	}

	return os.WriteFile(filePath, []byte(strings.Join(result, "\n")), 0o644)
}

// --- Note Dedup Tool Handler ---

func toolNoteDedup(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		AutoDelete bool   `json:"auto_delete"`
		Prefix     string `json:"prefix"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	svc := getGlobalNotesService()
	if svc == nil {
		return "", fmt.Errorf("notes service is not enabled")
	}

	vaultPath := svc.VaultPath()

	type fileHash struct {
		Path string
		Hash string
		Size int64
	}
	var files []fileHash
	hashMap := make(map[string][]string)

	filepath.Walk(vaultPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}
		if args.Prefix != "" {
			rel, _ := filepath.Rel(vaultPath, path)
			if !strings.HasPrefix(rel, args.Prefix) {
				return nil
			}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		h := sha256.Sum256(data)
		hash := hex.EncodeToString(h[:16])
		rel, _ := filepath.Rel(vaultPath, path)
		files = append(files, fileHash{Path: rel, Hash: hash, Size: info.Size()})
		hashMap[hash] = append(hashMap[hash], rel)
		return nil
	})

	var duplicates []map[string]any
	deleted := 0
	for hash, paths := range hashMap {
		if len(paths) <= 1 {
			continue
		}
		if args.AutoDelete {
			for _, p := range paths[1:] {
				fullPath := filepath.Join(vaultPath, p)
				if err := os.Remove(fullPath); err == nil {
					deleted++
				}
			}
		}
		duplicates = append(duplicates, map[string]any{
			"hash":  hash,
			"files": paths,
			"count": len(paths),
		})
	}

	result := map[string]any{
		"total_files":      len(files),
		"duplicate_groups": len(duplicates),
		"duplicates":       duplicates,
	}
	if args.AutoDelete {
		result["deleted"] = deleted
	}

	b, _ := json.Marshal(result)
	log.InfoCtx(ctx, "note dedup scan complete", "total_files", len(files), "duplicate_groups", len(duplicates))
	return string(b), nil
}

// --- Source Audit Tool Handler ---

func toolSourceAudit(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Expected []string `json:"expected"`
		Prefix   string   `json:"prefix"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	svc := getGlobalNotesService()
	if svc == nil {
		return "", fmt.Errorf("notes service is not enabled")
	}

	vaultPath := svc.VaultPath()
	prefix := args.Prefix
	if prefix == "" {
		prefix = "."
	}

	actualSet := make(map[string]bool)
	scanDir := filepath.Join(vaultPath, prefix)
	filepath.Walk(scanDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		rel, _ := filepath.Rel(vaultPath, path)
		actualSet[rel] = true
		return nil
	})

	expectedSet := make(map[string]bool)
	for _, e := range args.Expected {
		expectedSet[e] = true
	}

	var missing, extra []string
	for e := range expectedSet {
		if !actualSet[e] {
			missing = append(missing, e)
		}
	}
	for a := range actualSet {
		if !expectedSet[a] {
			extra = append(extra, a)
		}
	}

	result := map[string]any{
		"expected_count": len(args.Expected),
		"actual_count":   len(actualSet),
		"missing_count":  len(missing),
		"extra_count":    len(extra),
		"missing":        missing,
		"extra":          extra,
	}
	b, _ := json.Marshal(result)
	log.InfoCtx(ctx, "source audit complete", "expected", len(args.Expected), "actual", len(actualSet))
	return string(b), nil
}

