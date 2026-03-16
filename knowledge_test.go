package main

import (
	"os"
	"path/filepath"
	"testing"

	"tetora/internal/knowledge"
)

func TestInitKnowledgeDir(t *testing.T) {
	dir := t.TempDir()
	kDir := knowledge.InitDir(dir)
	want := filepath.Join(dir, "knowledge")
	if kDir != want {
		t.Errorf("InitDir = %q, want %q", kDir, want)
	}
	if _, err := os.Stat(kDir); err != nil {
		t.Errorf("knowledge dir not created: %v", err)
	}
}

func TestInitKnowledgeDirIdempotent(t *testing.T) {
	dir := t.TempDir()
	knowledge.InitDir(dir)
	kDir := knowledge.InitDir(dir)
	if _, err := os.Stat(kDir); err != nil {
		t.Errorf("knowledge dir not found on second call: %v", err)
	}
}

func TestListKnowledgeFilesEmpty(t *testing.T) {
	dir := knowledge.InitDir(t.TempDir())
	files, err := knowledge.ListFiles(dir)
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 files, got %d", len(files))
	}
}

func TestListKnowledgeFilesNonExistent(t *testing.T) {
	files, err := knowledge.ListFiles("/nonexistent/path/knowledge")
	if err != nil {
		t.Fatalf("expected nil error for nonexistent dir, got: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 files, got %d", len(files))
	}
}

func TestListKnowledgeFilesSkipsHidden(t *testing.T) {
	dir := knowledge.InitDir(t.TempDir())
	os.WriteFile(filepath.Join(dir, ".hidden"), []byte("secret"), 0o644)
	os.WriteFile(filepath.Join(dir, "visible.md"), []byte("content"), 0o644)

	files, err := knowledge.ListFiles(dir)
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0].Name != "visible.md" {
		t.Errorf("expected visible.md, got %q", files[0].Name)
	}
}

func TestListKnowledgeFilesSkipsDirs(t *testing.T) {
	dir := knowledge.InitDir(t.TempDir())
	os.MkdirAll(filepath.Join(dir, "subdir"), 0o755)
	os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello"), 0o644)

	files, err := knowledge.ListFiles(dir)
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
}

func TestAddKnowledgeFile(t *testing.T) {
	baseDir := t.TempDir()
	kDir := knowledge.InitDir(baseDir)

	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "notes.md")
	os.WriteFile(srcPath, []byte("# Knowledge Notes"), 0o644)

	if err := knowledge.AddFile(kDir, srcPath); err != nil {
		t.Fatalf("AddFile: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(kDir, "notes.md"))
	if err != nil {
		t.Fatalf("read copied file: %v", err)
	}
	if string(data) != "# Knowledge Notes" {
		t.Errorf("copied content = %q, want %q", string(data), "# Knowledge Notes")
	}
}

func TestAddKnowledgeFileNotFound(t *testing.T) {
	kDir := knowledge.InitDir(t.TempDir())
	err := knowledge.AddFile(kDir, "/nonexistent/file.txt")
	if err == nil {
		t.Fatal("expected error for nonexistent source")
	}
}

func TestAddKnowledgeFileDirectory(t *testing.T) {
	kDir := knowledge.InitDir(t.TempDir())
	srcDir := t.TempDir()
	err := knowledge.AddFile(kDir, srcDir)
	if err == nil {
		t.Fatal("expected error when source is a directory")
	}
}

func TestAddKnowledgeFileHiddenReject(t *testing.T) {
	kDir := knowledge.InitDir(t.TempDir())
	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, ".secret")
	os.WriteFile(srcPath, []byte("secret"), 0o644)

	err := knowledge.AddFile(kDir, srcPath)
	if err == nil {
		t.Fatal("expected error for hidden file")
	}
}

func TestRemoveKnowledgeFile(t *testing.T) {
	kDir := knowledge.InitDir(t.TempDir())
	os.WriteFile(filepath.Join(kDir, "old.txt"), []byte("data"), 0o644)

	if err := knowledge.RemoveFile(kDir, "old.txt"); err != nil {
		t.Fatalf("RemoveFile: %v", err)
	}

	if _, err := os.Stat(filepath.Join(kDir, "old.txt")); !os.IsNotExist(err) {
		t.Error("file should have been removed")
	}
}

func TestRemoveKnowledgeFileNotFound(t *testing.T) {
	kDir := knowledge.InitDir(t.TempDir())
	err := knowledge.RemoveFile(kDir, "nonexistent.txt")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestRemoveKnowledgeFilePathTraversal(t *testing.T) {
	kDir := knowledge.InitDir(t.TempDir())
	err := knowledge.RemoveFile(kDir, "../../../etc/passwd")
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
}

func TestValidateKnowledgeFilename(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{"notes.md", false},
		{"README.txt", false},
		{"my-doc.pdf", false},
		{"", true},
		{".hidden", true},
		{"../etc/passwd", true},
		{"foo/bar.txt", true},
		{"foo\\bar.txt", true},
		{"..", true},
		{".", true},
	}
	for _, tc := range tests {
		err := knowledge.ValidateFilename(tc.name)
		if (err != nil) != tc.wantErr {
			t.Errorf("ValidateFilename(%q): err=%v, wantErr=%v", tc.name, err, tc.wantErr)
		}
	}
}

func TestKnowledgeDirHasFiles(t *testing.T) {
	kDir := knowledge.InitDir(t.TempDir())

	if knowledge.HasFiles(kDir) {
		t.Error("expected false for empty dir")
	}

	os.WriteFile(filepath.Join(kDir, ".hidden"), []byte("x"), 0o644)
	if knowledge.HasFiles(kDir) {
		t.Error("expected false with only hidden files")
	}

	os.WriteFile(filepath.Join(kDir, "doc.md"), []byte("content"), 0o644)
	if !knowledge.HasFiles(kDir) {
		t.Error("expected true with visible file")
	}
}

func TestKnowledgeDirHasFilesNonExistent(t *testing.T) {
	if knowledge.HasFiles("/nonexistent/knowledge") {
		t.Error("expected false for nonexistent dir")
	}
}

func TestKnowledgeDir(t *testing.T) {
	cfg := &Config{baseDir: "/tmp/tetora"}

	if got := knowledgeDir(cfg); got != "/tmp/tetora/knowledge" {
		t.Errorf("knowledgeDir (default) = %q, want %q", got, "/tmp/tetora/knowledge")
	}

	cfg.KnowledgeDir = "/custom/knowledge"
	if got := knowledgeDir(cfg); got != "/custom/knowledge" {
		t.Errorf("knowledgeDir (custom) = %q, want %q", got, "/custom/knowledge")
	}
}

func TestFormatSizeKnowledge(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{2048, "2.0 KB"},
		{1048576, "1.0 MB"},
		{1572864, "1.5 MB"},
	}
	for _, tc := range tests {
		got := formatSize(tc.bytes)
		if got != tc.want {
			t.Errorf("formatSize(%d) = %q, want %q", tc.bytes, got, tc.want)
		}
	}
}

func TestExpandPromptKnowledgeDir(t *testing.T) {
	got := expandPrompt("Use files in {{knowledge_dir}}", "", "", "", "/tmp/tetora/knowledge", nil)
	want := "Use files in /tmp/tetora/knowledge"
	if got != want {
		t.Errorf("expandPrompt with knowledge_dir = %q, want %q", got, want)
	}
}

func TestExpandPromptKnowledgeDirEmpty(t *testing.T) {
	got := expandPrompt("Use files in {{knowledge_dir}}", "", "", "", "", nil)
	want := "Use files in "
	if got != want {
		t.Errorf("expandPrompt with empty knowledge_dir = %q, want %q", got, want)
	}
}
