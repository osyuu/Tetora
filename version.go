package main

import (
	"encoding/json"
	"fmt"

	"tetora/internal/version"
)

// ConfigVersion is an alias for version.Version so that existing callers in
// the root package continue to compile without modification.
type ConfigVersion = version.Version

// --- DB Init ---

func initVersionDB(dbPath string) error { return version.InitDB(dbPath) }

// --- Version ID ---

func newVersionID() string { return version.NewID() }

// --- Snapshot ---

func snapshotConfig(dbPath, configPath, changedBy, reason string) error {
	return version.SnapshotConfig(dbPath, configPath, changedBy, reason)
}

func snapshotWorkflow(dbPath, workflowName, content, changedBy, reason string) error {
	return version.SnapshotWorkflow(dbPath, workflowName, content, changedBy, reason)
}

func snapshotPrompt(dbPath, promptName, content, changedBy, reason string) error {
	return version.SnapshotPrompt(dbPath, promptName, content, changedBy, reason)
}

func snapshotEntity(dbPath, entityType, entityName, content, changedBy, reason string) error {
	return version.SnapshotEntity(dbPath, entityType, entityName, content, changedBy, reason)
}

// --- Query ---

func queryLatestVersion(dbPath, entityType, entityName string) (*ConfigVersion, error) {
	return version.QueryLatest(dbPath, entityType, entityName)
}

func queryVersions(dbPath, entityType, entityName string, limit int) ([]ConfigVersion, error) {
	return version.QueryVersions(dbPath, entityType, entityName, limit)
}

func queryVersionByID(dbPath, versionID string) (*ConfigVersion, error) {
	return version.QueryByID(dbPath, versionID)
}

func queryAllVersionedEntities(dbPath string) ([]ConfigVersion, error) {
	return version.QueryAllEntities(dbPath)
}

// --- Restore ---

func restoreConfigVersion(dbPath, configPath, versionID string) (string, error) {
	return version.RestoreConfig(dbPath, configPath, versionID)
}

// restoreWorkflowVersion restores a workflow to a saved version.
// Kept in the root package because it depends on root types: Workflow, Config,
// loadWorkflowByName, and saveWorkflow.
func restoreWorkflowVersion(dbPath string, cfg *Config, versionID string) error {
	ver, err := version.QueryByID(dbPath, versionID)
	if err != nil {
		return err
	}
	if ver.EntityType != "workflow" {
		return fmt.Errorf("version %q is a %s, not a workflow", versionID, ver.EntityType)
	}

	// Validate the stored content.
	var wf Workflow
	if err := json.Unmarshal([]byte(ver.ContentJSON), &wf); err != nil {
		return fmt.Errorf("stored version has invalid workflow JSON: %w", err)
	}

	// Read current workflow for backup snapshot (if it exists).
	existing, err := loadWorkflowByName(cfg, ver.EntityName)
	if err == nil && existing != nil {
		data, _ := json.MarshalIndent(existing, "", "  ")
		version.SnapshotEntity(dbPath, "workflow", ver.EntityName, string(data), "system", fmt.Sprintf("pre-rollback to %s", versionID))
	}

	// Save the restored workflow.
	if err := saveWorkflow(cfg, &wf); err != nil {
		return fmt.Errorf("write restored workflow: %w", err)
	}

	// Snapshot the restored state.
	version.SnapshotEntity(dbPath, "workflow", ver.EntityName, ver.ContentJSON, "system", fmt.Sprintf("rollback to %s", versionID))

	return nil
}

// --- Diff ---

func versionDiffDetail(dbPath, versionID1, versionID2 string) (map[string]any, error) {
	return version.DiffDetail(dbPath, versionID1, versionID2)
}

// --- Prune ---

func pruneVersions(dbPath, entityType, entityName string, keep int) {
	version.Prune(dbPath, entityType, entityName, keep)
}

func cleanupVersions(dbPath string, days int) {
	version.Cleanup(dbPath, days)
}
