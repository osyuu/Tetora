package scheduling

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBackupScheduler_RunBackup(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	backupDir := filepath.Join(dir, "backups")

	// Create a test database with some data.
	exec.Command("sqlite3", dbPath, "CREATE TABLE test(id INTEGER); INSERT INTO test VALUES(1);").Run()
	// Create backup_log table.
	exec.Command("sqlite3", dbPath,
		"CREATE TABLE IF NOT EXISTS backup_log (id INTEGER PRIMARY KEY AUTOINCREMENT, filename TEXT, size_bytes INTEGER, status TEXT, duration_ms INTEGER, created_at TEXT)").Run()

	bs := NewBackupScheduler(BackupConfig{
		DBPath:     dbPath,
		BackupDir:  backupDir,
		RetainDays: 7,
	})

	result, err := bs.RunBackup()
	if err != nil {
		t.Fatalf("RunBackup failed: %v", err)
	}

	if result.Filename == "" {
		t.Error("expected filename")
	}
	if result.SizeBytes <= 0 {
		t.Error("expected positive size")
	}
	if result.DurationMs < 0 {
		t.Error("expected non-negative duration")
	}
	if result.CreatedAt == "" {
		t.Error("expected createdAt")
	}

	if _, err := os.Stat(result.Filename); err != nil {
		t.Fatalf("backup file does not exist: %v", err)
	}
}

func TestBackupScheduler_RunBackupNoHistoryDB(t *testing.T) {
	bs := NewBackupScheduler(BackupConfig{DBPath: ""})

	_, err := bs.RunBackup()
	if err == nil {
		t.Error("expected error for empty historyDB")
	}
	if !strings.Contains(err.Error(), "historyDB not configured") {
		t.Errorf("expected historyDB error, got: %v", err)
	}
}

func TestBackupScheduler_CleanOldBackups(t *testing.T) {
	dir := t.TempDir()
	backupDir := filepath.Join(dir, "backups")
	os.MkdirAll(backupDir, 0o755)

	oldFile := filepath.Join(backupDir, "20250101-000000_tetora.db.bak")
	os.WriteFile(oldFile, []byte("old backup"), 0o644)
	oldTime := time.Now().AddDate(0, 0, -30)
	os.Chtimes(oldFile, oldTime, oldTime)

	newFile := filepath.Join(backupDir, "20260223-000000_tetora.db.bak")
	os.WriteFile(newFile, []byte("new backup"), 0o644)

	otherFile := filepath.Join(backupDir, "notes.txt")
	os.WriteFile(otherFile, []byte("notes"), 0o644)
	os.Chtimes(otherFile, oldTime, oldTime)

	bs := NewBackupScheduler(BackupConfig{
		BackupDir:  backupDir,
		RetainDays: 7,
	})

	removed := bs.CleanOldBackups()
	if removed != 1 {
		t.Errorf("expected 1 removed, got %d", removed)
	}

	if _, err := os.Stat(oldFile); err == nil {
		t.Error("old backup should have been removed")
	}
	if _, err := os.Stat(newFile); err != nil {
		t.Error("new backup should remain")
	}
	if _, err := os.Stat(otherFile); err != nil {
		t.Error("non-backup file should remain")
	}
}

func TestBackupScheduler_ListBackups(t *testing.T) {
	dir := t.TempDir()
	backupDir := filepath.Join(dir, "backups")
	os.MkdirAll(backupDir, 0o755)

	os.WriteFile(filepath.Join(backupDir, "20260101-000000_tetora.db.bak"), []byte("backup1"), 0o644)
	os.WriteFile(filepath.Join(backupDir, "20260201-000000_tetora.db.bak"), []byte("backup22"), 0o644)
	os.WriteFile(filepath.Join(backupDir, "random.txt"), []byte("not a backup"), 0o644)

	bs := NewBackupScheduler(BackupConfig{
		BackupDir: backupDir,
	})

	backups, err := bs.ListBackups()
	if err != nil {
		t.Fatalf("ListBackups failed: %v", err)
	}

	if len(backups) != 2 {
		t.Fatalf("expected 2 backups, got %d", len(backups))
	}

	if !strings.Contains(backups[0].Filename, "20260201") {
		t.Errorf("expected newest first, got %s", backups[0].Filename)
	}
}

func TestBackupScheduler_ListBackupsEmptyDir(t *testing.T) {
	dir := t.TempDir()

	bs := NewBackupScheduler(BackupConfig{
		BackupDir: filepath.Join(dir, "nonexistent"),
	})

	backups, err := bs.ListBackups()
	if err != nil {
		t.Fatalf("ListBackups failed: %v", err)
	}
	if len(backups) != 0 {
		t.Errorf("expected 0 backups, got %d", len(backups))
	}
}

func TestCopyFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")

	content := "hello world"
	os.WriteFile(src, []byte(content), 0o644)

	err := CopyFile(src, dst)
	if err != nil {
		t.Fatalf("CopyFile failed: %v", err)
	}

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst failed: %v", err)
	}
	if string(data) != content {
		t.Errorf("expected %q, got %q", content, string(data))
	}
}

func TestCopyFile_SourceNotExists(t *testing.T) {
	dir := t.TempDir()
	err := CopyFile(filepath.Join(dir, "nonexistent"), filepath.Join(dir, "dst"))
	if err == nil {
		t.Error("expected error for missing source")
	}
}
