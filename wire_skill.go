package main

import (
	"context"
	"encoding/json"

	"tetora/internal/classify"
	"tetora/internal/skill"
)

// --- Type aliases (non-config) ---
// Config type aliases (SkillConfig, SkillStoreConfig) are now in config.go.

type SkillResult = skill.SkillResult
type SkillMetadata = skill.SkillMetadata
type SkillMatcher = skill.SkillMatcher
type SkillEventOpts = skill.SkillEventOpts
type SentoriReport = skill.SentoriReport
type SentoriFinding = skill.SentoriFinding
type SkillRegistryEntry = skill.SkillRegistryEntry

// --- Constants ---

const skillFailuresMaxInject = skill.SkillFailuresMaxInject

// --- Adapters ---

// toSkillAppConfig adapts *Config to *skill.AppConfig.
func toSkillAppConfig(cfg *Config) *skill.AppConfig {
	maxSkills := cfg.PromptBudget.MaxSkillsPerTask
	if maxSkills <= 0 {
		maxSkills = 3
	}
	skillsMax := cfg.PromptBudget.SkillsMax
	if skillsMax <= 0 {
		skillsMax = 4000
	}
	return &skill.AppConfig{
		Skills:           cfg.Skills,
		SkillStore:       cfg.SkillStore,
		WorkspaceDir:     cfg.WorkspaceDir,
		HistoryDB:        cfg.HistoryDB,
		BaseDir:          cfg.BaseDir,
		MaxSkillsPerTask: maxSkills,
		SkillsMax:        skillsMax,
		Browser:          globalBrowserRelay,
	}
}

// toSkillTask converts a Task to skill.TaskContext.
func toSkillTask(t Task) skill.TaskContext {
	return skill.TaskContext{
		Agent:     t.Agent,
		Prompt:    t.Prompt,
		Source:    t.Source,
		SessionID: t.SessionID,
	}
}

// --- Skill registry / lookup ---

func listSkills(cfg *Config) []SkillConfig {
	return skill.ListSkills(toSkillAppConfig(cfg))
}

func getSkill(cfg *Config, name string) *SkillConfig {
	return skill.GetSkill(toSkillAppConfig(cfg), name)
}

func executeSkill(ctx context.Context, s SkillConfig, vars map[string]string) (*SkillResult, error) {
	return skill.ExecuteSkill(ctx, s, vars)
}

func testSkill(ctx context.Context, s SkillConfig) (*SkillResult, error) {
	return skill.TestSkill(ctx, s)
}

func expandSkillVars(s string, vars map[string]string) string {
	return skill.ExpandSkillVars(s, vars)
}

// --- Skill creation / management ---

func isValidSkillName(name string) bool {
	return skill.IsValidSkillName(name)
}

func skillsDir(cfg *Config) string {
	return skill.SkillsDir(toSkillAppConfig(cfg))
}

func createSkill(cfg *Config, meta SkillMetadata, script string) error {
	return skill.CreateSkill(toSkillAppConfig(cfg), meta, script)
}

func loadFileSkills(cfg *Config) []SkillConfig {
	return skill.LoadFileSkills(toSkillAppConfig(cfg))
}

func loadAllFileSkillMetas(cfg *Config) []SkillMetadata {
	return skill.LoadAllFileSkillMetas(toSkillAppConfig(cfg))
}

func mergeSkills(configSkills, fileSkills []SkillConfig) []SkillConfig {
	return skill.MergeSkills(configSkills, fileSkills)
}

func approveSkill(cfg *Config, name string) error {
	return skill.ApproveSkill(toSkillAppConfig(cfg), name)
}

func rejectSkill(cfg *Config, name string) error {
	return skill.RejectSkill(toSkillAppConfig(cfg), name)
}

func deleteFileSkill(cfg *Config, name string) error {
	return skill.DeleteFileSkill(toSkillAppConfig(cfg), name)
}

func recordSkillUsage(cfg *Config, name string) {
	skill.RecordSkillUsage(toSkillAppConfig(cfg), name)
}

func listPendingSkills(cfg *Config) []SkillMetadata {
	return skill.ListPendingSkills(toSkillAppConfig(cfg))
}

func createSkillToolHandler(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	return skill.CreateSkillToolHandler(ctx, toSkillAppConfig(cfg), input)
}

// --- Failure tracking ---

func appendSkillFailure(cfg *Config, skillName, taskTitle, agentName, errMsg string) {
	skill.AppendSkillFailure(toSkillAppConfig(cfg), skillName, taskTitle, agentName, errMsg)
}

func loadSkillFailures(skillDir string) string {
	return skill.LoadSkillFailures(skillDir)
}

func loadSkillFailuresByName(cfg *Config, skillName string) string {
	return skill.LoadSkillFailuresByName(toSkillAppConfig(cfg), skillName)
}

func parseFailureEntries(fpath string) []string {
	return skill.ParseFailureEntries(fpath)
}

// --- Skill injection ---

func selectSkills(cfg *Config, task Task) []SkillConfig {
	return skill.SelectSkills(toSkillAppConfig(cfg), toSkillTask(task))
}

func shouldInjectSkill(s SkillConfig, task Task) bool {
	return skill.ShouldInjectSkill(s, toSkillTask(task))
}

func buildSkillsPrompt(cfg *Config, task Task, complexity classify.Complexity) string {
	return skill.BuildSkillsPrompt(toSkillAppConfig(cfg), toSkillTask(task), complexity)
}

func skillMatchesContext(s SkillConfig, role, prompt, source string) bool {
	return skill.SkillMatchesContext(s, role, prompt, source)
}

func extractChannelFromSource(source string) string {
	return skill.ExtractChannelFromSource(source)
}

func autoInjectLearnedSkills(cfg *Config, task Task) []SkillConfig {
	return skill.AutoInjectLearnedSkills(toSkillAppConfig(cfg), toSkillTask(task))
}

// --- Skill learning / analytics ---

func initSkillUsageTable(dbPath string) error {
	return skill.InitSkillUsageTable(dbPath)
}

func recordSkillEvent(dbPath, skillName, eventType, taskPrompt, role string) {
	skill.RecordSkillEvent(dbPath, skillName, eventType, taskPrompt, role)
}

func recordSkillEventEx(dbPath, skillName, eventType, taskPrompt, role string, opts SkillEventOpts) {
	skill.RecordSkillEventEx(dbPath, skillName, eventType, taskPrompt, role, opts)
}

func querySkillStats(dbPath string, skillName string, days int) ([]map[string]any, error) {
	return skill.QuerySkillStats(dbPath, skillName, days)
}

func querySkillHistory(dbPath, skillName string, limit int) ([]map[string]any, error) {
	return skill.QuerySkillHistory(dbPath, skillName, limit)
}

func suggestSkillsForPrompt(dbPath, prompt string, limit int) []string {
	return skill.SuggestSkillsForPrompt(dbPath, prompt, limit)
}

func skillTokenize(text string) []string {
	return skill.SkillTokenize(text)
}

func recordSkillCompletion(dbPath string, task Task, result TaskResult, role, startedAt, finishedAt string) {
	skill.RecordSkillCompletion(dbPath, skill.TaskContext{
		Agent:     task.Agent,
		Prompt:    task.Prompt,
		Source:    task.Source,
		SessionID: task.SessionID,
	}, skill.TaskCompletion{
		Status: result.Status,
		Error:  result.Error,
	}, role, startedAt, finishedAt)
}

// --- Install / security ---

func sentoriScan(skillName, content string) *SentoriReport {
	return skill.SentoriScan(skillName, content)
}

func loadFileSkillScript(cfg *Config, name string) (string, error) {
	return skill.LoadFileSkillScript(toSkillAppConfig(cfg), name)
}

func toolSentoriScan(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	return skill.ToolSentoriScan(ctx, toSkillAppConfig(cfg), input)
}

func toolSkillInstall(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	return skill.ToolSkillInstall(ctx, toSkillAppConfig(cfg), input)
}

func toolSkillSearch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	return skill.ToolSkillSearch(ctx, toSkillAppConfig(cfg), input)
}

// --- NotebookLM ---

func toolNotebookLMImport(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	return skill.ToolNotebookLMImport(ctx, toSkillAppConfig(cfg), input)
}

func toolNotebookLMListSources(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	return skill.ToolNotebookLMListSources(ctx, toSkillAppConfig(cfg), input)
}

func toolNotebookLMQuery(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	return skill.ToolNotebookLMQuery(ctx, toSkillAppConfig(cfg), input)
}

func toolNotebookLMDeleteSource(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	return skill.ToolNotebookLMDeleteSource(ctx, toSkillAppConfig(cfg), input)
}

// --- Diagnostics ---

func skillDiagnosticsCmd(args []string) {
	cfg := loadConfig(findConfigPath())
	skill.SkillDiagnosticsCmd(args, cfg.HistoryDB)
}
