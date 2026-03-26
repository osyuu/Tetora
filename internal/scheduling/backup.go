package scheduling

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// BackupConfig holds the configuration needed by BackupScheduler,
// decoupled from the root Config type.
type BackupConfig struct {
	DBPath     string
	BackupDir  string
	RetainDays int
	EscapeSQL  func(string) string
	LogInfo    LogFunc
	LogWarn    LogFunc
}

// BackupScheduler manages periodic database backups.
type BackupScheduler struct {
	cfg BackupConfig
}

// BackupResult describes the result of a backup operation.
type BackupResult struct {
	Filename   string `json:"filename"`
	SizeBytes  int64  `json:"sizeBytes"`
	DurationMs int64  `json:"durationMs"`
	CreatedAt  string `json:"createdAt"`
}

// BackupInfo describes a stored backup file.
type BackupInfo struct {
	Filename  string `json:"filename"`
	SizeBytes int64  `json:"sizeBytes"`
	CreatedAt string `json:"createdAt"`
}

// NewBackupScheduler creates a new backup scheduler.
func NewBackupScheduler(cfg BackupConfig) *BackupScheduler {
	if cfg.LogInfo == nil {
		cfg.LogInfo = func(string, ...any) {}
	}
	if cfg.LogWarn == nil {
		cfg.LogWarn = func(string, ...any) {}
	}
	if cfg.EscapeSQL == nil {
		cfg.EscapeSQL = func(s string) string {
			return strings.ReplaceAll(s, "'", "''")
		}
	}
	return &BackupScheduler{cfg: cfg}
}

// RunBackup copies the database file to the backup directory.
func (bs *BackupScheduler) RunBackup() (*BackupResult, error) {
	if bs.cfg.DBPath == "" {
		return nil, fmt.Errorf("historyDB not configured")
	}

	start := time.Now()

	if err := os.MkdirAll(bs.cfg.BackupDir, 0o755); err != nil {
		return nil, fmt.Errorf("create backup dir: %w", err)
	}

	date := time.Now().UTC().Format("20060102-150405")
	backupFilename := fmt.Sprintf("%s_tetora.db.bak", date)
	backupPath := filepath.Join(bs.cfg.BackupDir, backupFilename)

	if err := CopyFile(bs.cfg.DBPath, backupPath); err != nil {
		bs.logBackup(backupFilename, 0, "failed", time.Since(start).Milliseconds())
		return nil, fmt.Errorf("copy database: %w", err)
	}

	if err := VerifyDBBackup(backupPath); err != nil {
		bs.logBackup(backupFilename, 0, "verify_failed", time.Since(start).Milliseconds())
		os.Remove(backupPath)
		return nil, fmt.Errorf("backup verification failed: %w", err)
	}

	info, err := os.Stat(backupPath)
	if err != nil {
		return nil, fmt.Errorf("stat backup: %w", err)
	}

	duration := time.Since(start).Milliseconds()

	bs.logBackup(backupFilename, info.Size(), "success", duration)

	result := &BackupResult{
		Filename:   backupPath,
		SizeBytes:  info.Size(),
		DurationMs: duration,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}

	bs.cfg.LogInfo("backup complete", "filename", backupFilename, "sizeBytes", info.Size(), "durationMs", duration)
	return result, nil
}

func (bs *BackupScheduler) logBackup(filename string, sizeBytes int64, status string, durationMs int64) {
	if bs.cfg.DBPath == "" {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	sql := fmt.Sprintf(
		`INSERT INTO backup_log (filename, size_bytes, status, duration_ms, created_at) VALUES ('%s', %d, '%s', %d, '%s')`,
		bs.cfg.EscapeSQL(filename), sizeBytes, bs.cfg.EscapeSQL(status), durationMs, now,
	)
	cmd := exec.Command("sqlite3", bs.cfg.DBPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		bs.cfg.LogWarn("backup log insert failed", "error", err, "output", string(out))
	}
}

// CleanOldBackups removes backup files older than the retention period.
func (bs *BackupScheduler) CleanOldBackups() int {
	retainDays := bs.cfg.RetainDays
	if retainDays <= 0 {
		retainDays = 7
	}
	cutoff := time.Now().AddDate(0, 0, -retainDays)

	entries, err := os.ReadDir(bs.cfg.BackupDir)
	if err != nil {
		return 0
	}

	removed := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".db.bak") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		if info.ModTime().Before(cutoff) {
			path := filepath.Join(bs.cfg.BackupDir, entry.Name())
			if err := os.Remove(path); err != nil {
				bs.cfg.LogWarn("remove old backup failed", "file", entry.Name(), "error", err)
			} else {
				removed++
			}
		}
	}

	if removed > 0 {
		bs.cfg.LogInfo("old backups cleaned", "removed", removed, "retainDays", retainDays)
	}
	return removed
}

// ListBackups returns all backup files sorted by creation time (newest first).
func (bs *BackupScheduler) ListBackups() ([]BackupInfo, error) {
	entries, err := os.ReadDir(bs.cfg.BackupDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []BackupInfo{}, nil
		}
		return nil, fmt.Errorf("read backup dir: %w", err)
	}

	var backups []BackupInfo
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".db.bak") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		backups = append(backups, BackupInfo{
			Filename:  entry.Name(),
			SizeBytes: info.Size(),
			CreatedAt: info.ModTime().UTC().Format(time.RFC3339),
		})
	}

	sort.Slice(backups, func(i, j int) bool {
		return backups[i].Filename > backups[j].Filename
	})

	return backups, nil
}

// Start runs periodic backups based on a simplified schedule.
func (bs *BackupScheduler) Start(ctx context.Context) {
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}

		if _, err := bs.RunBackup(); err != nil {
			bs.cfg.LogWarn("initial backup failed", "error", err)
		}
		bs.CleanOldBackups()

		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := bs.RunBackup(); err != nil {
					bs.cfg.LogWarn("periodic backup failed", "error", err)
				}
				bs.CleanOldBackups()
			}
		}
	}()
}

// VerifyDBBackup runs sqlite3 integrity_check on a backup file.
func VerifyDBBackup(path string) error {
	cmd := exec.Command("sqlite3", path, "PRAGMA integrity_check;")
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("integrity_check: %w", err)
	}
	result := strings.TrimSpace(string(out))
	if result != "ok" {
		return fmt.Errorf("integrity_check: %s", result)
	}
	return nil
}

// CopyFile copies a file from src to dst using io.Copy.
func CopyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return err
	}

	return dstFile.Sync()
}
