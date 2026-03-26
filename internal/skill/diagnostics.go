package skill

// Native Go implementation of skill trigger diagnostics.
// Replaces the python3 shim that called skills/scripts/skill_diagnostics.py.

import (
	"encoding/json"
	iofs "io/fs"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"tetora/internal/db"
)

// ── Config ──

var diagSkillDirs = []string{
	filepath.Join(os.Getenv("HOME"), ".claude", "skills"),
	filepath.Join(os.Getenv("HOME"), ".tetora", "workspace", "skills"),
}

var diagActionVerbs = map[string]bool{
	"use": true, "trigger": true, "activate": true, "invoke": true,
	"run": true, "execute": true, "create": true, "build": true,
	"generate": true, "write": true, "add": true, "update": true,
	"check": true, "verify": true, "validate": true, "test": true,
	"review": true, "analyze": true, "fetch": true, "extract": true,
	"download": true, "deploy": true, "release": true, "publish": true,
}

var diagStopWords = map[string]bool{
	"the": true, "a": true, "an": true, "is": true, "are": true, "was": true,
	"were": true, "be": true, "been": true, "have": true, "has": true, "had": true,
	"do": true, "does": true, "did": true, "will": true, "would": true, "could": true,
	"should": true, "may": true, "might": true, "shall": true, "can": true, "need": true,
	"this": true, "that": true, "these": true, "those": true, "it": true, "its": true,
	"they": true, "them": true, "for": true, "and": true, "but": true, "or": true,
	"nor": true, "not": true, "no": true, "so": true, "if": true, "then": true,
	"than": true, "when": true, "where": true, "how": true, "what": true, "which": true,
	"who": true, "with": true, "from": true, "into": true, "onto": true, "upon": true,
	"about": true, "after": true, "before": true, "between": true, "through": true,
	"during": true, "without": true, "within": true, "of": true, "in": true, "on": true,
	"at": true, "to": true, "by": true, "as": true, "up": true, "out": true, "any": true,
	"all": true, "each": true, "every": true, "both": true, "few": true, "more": true,
	"most": true, "other": true, "some": true, "such": true, "only": true, "own": true,
	"same": true, "also": true,
}

// ── Types ──

type diagSkill struct {
	Path        string
	Name        string
	Description string
	Triggers    []string
	Requires    []string // populated from "requires" or "compatibility" (aliases)
	Version     string
	Maintainer  string
	BodyLines   int
	SourceDir   string
}

type diagQuality struct {
	Name           string
	Score          int
	Issues         []string
	DescriptionLen int
	BodyLines      int
}

type diagOverlap struct {
	SkillA       string
	SkillB       string
	Jaccard      float64
	SharedTokens []string
}

// ── YAML frontmatter parser ──

func diagParseSkillMD(path string) (*diagSkill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := string(data)

	s := &diagSkill{
		Path: path,
		Name: filepath.Base(filepath.Dir(path)),
	}

	if !strings.HasPrefix(text, "---") {
		s.BodyLines = strings.Count(text, "\n") + 1
		return s, nil
	}

	parts := strings.SplitN(text, "---", 3)
	if len(parts) < 3 {
		return nil, fmt.Errorf("invalid SKILL.md: %s", path)
	}

	frontmatter := strings.TrimSpace(parts[1])
	body := strings.TrimSpace(parts[2])
	s.BodyLines = strings.Count(body, "\n") + 1

	var currentKey string
	var inMetadata bool
	for _, line := range strings.Split(frontmatter, "\n") {
		isIndented := len(line) > 0 && (line[0] == ' ' || line[0] == '\t')
		stripped := strings.TrimSpace(line)
		if stripped == "" {
			continue
		}
		if strings.HasPrefix(stripped, "- ") {
			item := strings.TrimSpace(strings.TrimPrefix(stripped, "- "))
			item = strings.Trim(item, `"'`)
			if item != "" {
				switch currentKey {
				case "triggers":
					s.Triggers = append(s.Triggers, item)
				case "requires", "compatibility":
					s.Requires = diagAppendUnique(s.Requires, item)
				}
			}
			continue
		}
		if idx := strings.Index(stripped, ":"); idx > 0 {
			key := strings.TrimSpace(stripped[:idx])
			val := strings.TrimSpace(stripped[idx+1:])
			val = strings.Trim(val, `"'`)

			// Handle metadata: sub-keys (version, maintainer)
			if isIndented && inMetadata {
				switch key {
				case "version":
					s.Version = val
				case "maintainer":
					s.Maintainer = val
				}
				continue
			}

			if !isIndented {
				inMetadata = false
			}
			currentKey = key
			switch key {
			case "name":
				if val != "" {
					s.Name = val
				}
			case "description":
				s.Description = val
			case "version":
				s.Version = val // backward compat: top-level still works
			case "maintainer":
				s.Maintainer = val // backward compat
			case "metadata":
				inMetadata = true
			case "triggers":
				if strings.HasPrefix(val, "[") {
					val = strings.Trim(val, "[]")
					for _, t := range strings.Split(val, ",") {
						t = strings.TrimSpace(strings.Trim(t, `"'`))
						if t != "" {
							s.Triggers = append(s.Triggers, t)
						}
					}
				}
				// else multi-line block — items follow as "- " lines
			case "requires", "compatibility":
				if strings.HasPrefix(val, "[") {
					val = strings.Trim(val, "[]")
					for _, item := range strings.Split(val, ",") {
						item = strings.TrimSpace(strings.Trim(item, `"'`))
						if item != "" {
							s.Requires = diagAppendUnique(s.Requires, item)
						}
					}
				}
				// else multi-line block — items follow as "- " lines
			}
		}
	}

	return s, nil
}

// ── Discovery ──

func diagDiscoverSkills() []diagSkill {
	var skills []diagSkill
	seen := map[string]bool{}

	for _, baseDir := range diagSkillDirs {
		if _, err := os.Stat(baseDir); os.IsNotExist(err) {
			continue
		}
		filepath.WalkDir(baseDir, func(path string, d iofs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if d.Name() != "SKILL.md" {
				return nil
			}
			rel, _ := filepath.Rel(baseDir, path)
			parts := strings.Split(rel, string(filepath.Separator))
			if len(parts) > 2 {
				return nil // skip nested dirs
			}
			name := filepath.Base(filepath.Dir(path))
			if seen[name] {
				return nil
			}
			seen[name] = true
			s, err := diagParseSkillMD(path)
			if err != nil {
				return nil
			}
			s.SourceDir = baseDir
			skills = append(skills, *s)
			return nil
		})
	}

	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	return skills
}

// ── Tokenization ──

var diagTokenRe = regexp.MustCompile(`[a-z][a-z0-9_-]+`)

func diagTokenize(text string) map[string]bool {
	tokens := map[string]bool{}
	for _, w := range diagTokenRe.FindAllString(strings.ToLower(text), -1) {
		if !diagStopWords[w] && len(w) >= 3 {
			tokens[w] = true
		}
	}
	return tokens
}

// ── Quality scoring ──

func diagScoreDescription(s diagSkill) diagQuality {
	desc := s.Description
	var issues []string
	score := 100

	if len(desc) < 20 {
		issues = append(issues, "too short (< 20 chars)")
		score -= 30
	} else if len(desc) > 500 {
		issues = append(issues, "very long (> 500 chars) — increases system prompt cost")
		score -= 10
	}

	descLower := strings.ToLower(desc)

	hasAction := false
	for v := range diagActionVerbs {
		if strings.Contains(descLower, v) {
			hasAction = true
			break
		}
	}
	if !hasAction {
		issues = append(issues, "no action verb (use, trigger, create, etc.)")
		score -= 20
	}

	if !strings.Contains(descLower, "use when") &&
		!strings.Contains(descLower, "use this") &&
		!strings.Contains(descLower, "trigger when") {
		issues = append(issues, "missing 'Use when...' trigger context")
		score -= 10
	}

	if !strings.Contains(descLower, "do not") &&
		!strings.Contains(descLower, "don't") &&
		!strings.Contains(descLower, "not use") {
		issues = append(issues, "no negative trigger (DO NOT use for...)")
		score -= 5
	}

	if len(s.Triggers) > 0 {
		descTokens := diagTokenize(desc)
		trigTokens := map[string]bool{}
		for _, t := range s.Triggers {
			for w := range diagTokenize(t) {
				trigTokens[w] = true
			}
		}
		if len(trigTokens) > 0 {
			intersection := 0
			for w := range trigTokens {
				if descTokens[w] {
					intersection++
				}
			}
			coverage := float64(intersection) / float64(len(trigTokens))
			if coverage < 0.3 {
				issues = append(issues, fmt.Sprintf("trigger keywords poorly represented in description (%.0f%%)", coverage*100))
				score -= 15
			}
		}
	}

	if score < 0 {
		score = 0
	}
	return diagQuality{
		Name:           s.Name,
		Score:          score,
		Issues:         issues,
		DescriptionLen: len(desc),
		BodyLines:      s.BodyLines,
	}
}

// ── Overlap ──

func diagComputeOverlap(skills []diagSkill) []diagOverlap {
	cache := map[string]map[string]bool{}
	for _, s := range skills {
		combined := s.Description + " " + strings.Join(s.Triggers, " ")
		cache[s.Name] = diagTokenize(combined)
	}

	names := make([]string, 0, len(cache))
	for n := range cache {
		names = append(names, n)
	}
	sort.Strings(names)

	var overlaps []diagOverlap
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			a, b := cache[names[i]], cache[names[j]]
			if len(a) == 0 || len(b) == 0 {
				continue
			}
			union := map[string]bool{}
			for w := range a {
				union[w] = true
			}
			for w := range b {
				union[w] = true
			}
			var shared []string
			for w := range a {
				if b[w] {
					shared = append(shared, w)
				}
			}
			if len(union) == 0 {
				continue
			}
			jaccard := float64(len(shared)) / float64(len(union))
			if jaccard >= 0.3 {
				sort.Strings(shared)
				if len(shared) > 10 {
					shared = shared[:10]
				}
				overlaps = append(overlaps, diagOverlap{
					SkillA:       names[i],
					SkillB:       names[j],
					Jaccard:      jaccard,
					SharedTokens: shared,
				})
			}
		}
	}
	sort.Slice(overlaps, func(i, j int) bool { return overlaps[i].Jaccard > overlaps[j].Jaccard })
	return overlaps
}

// ── DB queries ──

func diagQueryUsageStats(dbPath string, days int) map[string]map[string]any {
	if dbPath == "" {
		return nil
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02T15:04:05Z")
	sql := fmt.Sprintf(`
		SELECT skill_name,
		       COUNT(CASE WHEN event_type = 'injected' THEN 1 END) AS injected,
		       COUNT(CASE WHEN event_type IN ('invoked', 'completed') THEN 1 END) AS invoked,
		       COUNT(CASE WHEN status = 'fail' OR event_type = 'failed' THEN 1 END) AS failed,
		       MAX(created_at) AS last_used
		FROM skill_usage
		WHERE created_at >= '%s'
		GROUP BY skill_name`, cutoff)
	rows, err := db.Query(dbPath, sql)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not query DB: %v\n", err)
		return nil
	}
	result := map[string]map[string]any{}
	for _, row := range rows {
		name := fmt.Sprintf("%v", row["skill_name"])
		result[name] = row
	}
	return result
}

func diagQueryPromptTerms(dbPath string, days int) map[string]int {
	if dbPath == "" {
		return nil
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02T15:04:05Z")
	sql := fmt.Sprintf(`
		SELECT task_prompt FROM skill_usage
		WHERE event_type = 'injected' AND task_prompt != ''
		AND created_at >= '%s'
		LIMIT 200`, cutoff)
	rows, err := db.Query(dbPath, sql)
	if err != nil {
		return nil
	}
	terms := map[string]int{}
	for _, row := range rows {
		prompt := fmt.Sprintf("%v", row["task_prompt"])
		for w := range diagTokenize(prompt) {
			terms[w]++
		}
	}
	return terms
}

// ── Report ──

func diagGenerateReport(
	skills []diagSkill,
	dbPath string,
	days int,
	asJSON bool,
	showScore, showOverlap, showDead, showGap bool,
) {
	showAll := !showScore && !showOverlap && !showDead && !showGap

	quality := make([]diagQuality, 0, len(skills))
	for _, s := range skills {
		quality = append(quality, diagScoreDescription(s))
	}
	sort.Slice(quality, func(i, j int) bool { return quality[i].Score < quality[j].Score })

	qualityByName := map[string]diagQuality{}
	for _, q := range quality {
		qualityByName[q.Name] = q
	}

	overlaps := diagComputeOverlap(skills)

	usage := diagQueryUsageStats(dbPath, days)
	var deadSkills []string
	for _, s := range skills {
		if usage == nil || usage[s.Name] == nil {
			deadSkills = append(deadSkills, s.Name)
		}
	}
	sort.Strings(deadSkills)

	promptTerms := diagQueryPromptTerms(dbPath, days)
	allSkillTokens := map[string]bool{}
	for _, s := range skills {
		for w := range diagTokenize(s.Description) {
			allSkillTokens[w] = true
		}
		for _, t := range s.Triggers {
			for w := range diagTokenize(t) {
				allSkillTokens[w] = true
			}
		}
	}
	gaps := map[string]int{}
	for term, count := range promptTerms {
		if !allSkillTokens[term] && count >= 3 {
			gaps[term] = count
		}
	}

	if asJSON {
		qualityJSON := make([]map[string]any, 0, len(quality))
		for _, q := range quality {
			qualityJSON = append(qualityJSON, map[string]any{
				"name": q.Name, "score": q.Score,
				"issues": q.Issues, "description_len": q.DescriptionLen,
				"body_lines": q.BodyLines,
			})
		}
		overlapsJSON := make([]map[string]any, 0, len(overlaps))
		for _, o := range overlaps {
			overlapsJSON = append(overlapsJSON, map[string]any{
				"skill_a": o.SkillA, "skill_b": o.SkillB,
				"jaccard": o.Jaccard, "shared_tokens": o.SharedTokens,
			})
		}
		usageJSON := map[string]any{}
		for k, v := range usage {
			usageJSON[k] = v
		}
		out, _ := json.MarshalIndent(map[string]any{
			"total_skills":  len(skills),
			"quality":       qualityJSON,
			"overlaps":      overlapsJSON,
			"dead_skills":   deadSkills,
			"coverage_gaps": gaps,
			"usage":         usageJSON,
		}, "", "  ")
		fmt.Println(string(out))
		return
	}

	fmt.Printf("# Skill Diagnostics Report\n\nTotal skills: %d (across %d directories)\nAnalysis period: last %d days\n\n",
		len(skills), len(diagSkillDirs), days)

	// 1. Inventory
	if showAll {
		fmt.Print("## 1. Skill Inventory\n\n")
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "Name\tScore\tLines\tInjected\tInvoked\tFailed\tLast Used")
		fmt.Fprintln(w, strings.Repeat("-", 80))
		for _, s := range skills {
			q := qualityByName[s.Name]
			u := usage[s.Name]
			lastUsed := "never"
			var injected, invoked, failed float64
			if u != nil {
				if v, ok := u["last_used"]; ok && v != nil {
					lastUsed = fmt.Sprintf("%v", v)
					if len(lastUsed) > 10 {
						lastUsed = lastUsed[:10]
					}
				}
				injected = diagFloat(u["injected"])
				invoked = diagFloat(u["invoked"])
				failed = diagFloat(u["failed"])
			}
			fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%d\t%d\t%s\n",
				s.Name, q.Score, s.BodyLines,
				int(injected), int(invoked), int(failed), lastUsed)
		}
		w.Flush()
		fmt.Println()
	}

	// 2. Quality issues
	if showAll || showScore {
		fmt.Print("## 2. Description Quality Issues\n\n")
		var problems []diagQuality
		for _, q := range quality {
			if len(q.Issues) > 0 {
				problems = append(problems, q)
			}
		}
		if len(problems) > 0 {
			for _, q := range problems {
				fmt.Printf("**%s** (score: %d)\n", q.Name, q.Score)
				for _, issue := range q.Issues {
					fmt.Printf("  - %s\n", issue)
				}
				fmt.Println()
			}
		} else {
			fmt.Print("All descriptions look good.\n\n")
		}
	}

	// 3. Overlap
	if showAll || showOverlap {
		fmt.Print("## 3. Trigger Overlap (Jaccard >= 0.3)\n\n")
		limit := overlaps
		if len(limit) > 15 {
			limit = limit[:15]
		}
		if len(limit) > 0 {
			for _, o := range limit {
				shared := o.SharedTokens
				if len(shared) > 5 {
					shared = shared[:5]
				}
				fmt.Printf("- **%s** <-> **%s** (%.0f%%) shared: %s\n",
					o.SkillA, o.SkillB, o.Jaccard*100, strings.Join(shared, ", "))
			}
			fmt.Println()
		} else {
			fmt.Print("No significant overlaps detected.\n\n")
		}
	}

	// 4. Dead skills
	if showAll || showDead {
		fmt.Printf("## 4. Dead Skills (no usage in %dd)\n\n", days)
		if len(deadSkills) > 0 {
			for _, name := range deadSkills {
				fmt.Printf("- %s\n", name)
			}
			fmt.Printf("\n%d/%d skills have no recorded usage.\n\n", len(deadSkills), len(skills))
		} else {
			fmt.Print("All skills have been used.\n\n")
		}
	}

	// 5. Coverage gaps
	if showAll || showGap {
		fmt.Print("## 5. Coverage Gaps\n\n")
		if len(gaps) > 0 {
			fmt.Print("Frequent prompt terms with no matching skill trigger:\n\n")
			type tc struct {
				term  string
				count int
			}
			var sorted []tc
			for t, c := range gaps {
				sorted = append(sorted, tc{t, c})
			}
			sort.Slice(sorted, func(i, j int) bool { return sorted[i].count > sorted[j].count })
			if len(sorted) > 15 {
				sorted = sorted[:15]
			}
			for _, x := range sorted {
				fmt.Printf("- **%s** (%d occurrences)\n", x.term, x.count)
			}
			fmt.Println()
		} else {
			fmt.Print("No significant coverage gaps found.\n\n")
		}
	}
}

// diagAppendUnique appends item to slice only if not already present.
func diagAppendUnique(slice []string, item string) []string {
	for _, s := range slice {
		if s == item {
			return slice
		}
	}
	return append(slice, item)
}

// diagFloat safely converts a JSON-decoded map value to float64.
func diagFloat(v any) float64 {
	switch val := v.(type) {
	case float64:
		return val
	case int:
		return float64(val)
	}
	return 0
}

// SkillDiagnosticsCmd is the entry point for `tetora skill diagnostics`.
// It is a pure Go replacement for the former python3 shim.
func SkillDiagnosticsCmd(args []string, historyDB string) {
	fset := flag.NewFlagSet("diagnostics", flag.ContinueOnError)
	days := fset.Int("days", 30, "analysis period in days")
	asJSON := fset.Bool("json", false, "output as JSON")
	showScore := fset.Bool("score", false, "show only description quality scores")
	showOverlap := fset.Bool("overlap", false, "show only trigger overlap analysis")
	showDead := fset.Bool("dead", false, "show only dead skills")
	showGap := fset.Bool("gap", false, "show only coverage gaps")
	if err := fset.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	skills := diagDiscoverSkills()
	if len(skills) == 0 {
		fmt.Fprintln(os.Stderr, "No skills found.")
		os.Exit(1)
	}

	diagGenerateReport(skills, historyDB, *days, *asJSON,
		*showScore, *showOverlap, *showDead, *showGap)
}
