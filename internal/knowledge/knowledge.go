// Package knowledge manages knowledge base files and semantic search.
package knowledge

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// File represents a file in the knowledge base directory.
type File struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	ModTime string `json:"modTime"`
}

// InitDir creates the knowledge base directory if it does not exist
// and returns the absolute path.
func InitDir(baseDir string) string {
	dir := filepath.Join(baseDir, "knowledge")
	os.MkdirAll(dir, 0o755)
	return dir
}

// ListFiles lists all files in the knowledge directory.
func ListFiles(knowledgeDir string) ([]File, error) {
	entries, err := os.ReadDir(knowledgeDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read knowledge dir: %w", err)
	}

	var files []File
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, File{
			Name:    e.Name(),
			Size:    info.Size(),
			ModTime: info.ModTime().Format(time.RFC3339),
		})
	}
	return files, nil
}

// AddFile copies a file from sourcePath into the knowledge directory.
func AddFile(knowledgeDir, sourcePath string) error {
	info, err := os.Stat(sourcePath)
	if err != nil {
		return fmt.Errorf("source file: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("source is a directory, not a file")
	}

	name := filepath.Base(sourcePath)
	if err := ValidateFilename(name); err != nil {
		return err
	}

	os.MkdirAll(knowledgeDir, 0o755)

	src, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	dstPath := filepath.Join(knowledgeDir, name)
	dst, err := os.Create(dstPath)
	if err != nil {
		return fmt.Errorf("create destination: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		os.Remove(dstPath)
		return fmt.Errorf("copy: %w", err)
	}
	return nil
}

// RemoveFile removes a file from the knowledge directory.
func RemoveFile(knowledgeDir, name string) error {
	if err := ValidateFilename(name); err != nil {
		return err
	}

	path := filepath.Join(knowledgeDir, name)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("file %q not found in knowledge base", name)
		}
		return err
	}
	return os.Remove(path)
}

// HasFiles returns true if the knowledge directory contains at least one non-hidden file.
func HasFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			return true
		}
	}
	return false
}

// ValidateFilename checks that a filename is safe (no path traversal, no hidden files).
func ValidateFilename(name string) error {
	if name == "" {
		return fmt.Errorf("filename is empty")
	}
	if strings.HasPrefix(name, ".") {
		return fmt.Errorf("hidden files not allowed: %q", name)
	}
	if strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("path separators not allowed in filename: %q", name)
	}
	if name == ".." || name == "." {
		return fmt.Errorf("invalid filename: %q", name)
	}
	if filepath.Clean(name) != name {
		return fmt.Errorf("unsafe filename: %q", name)
	}
	return nil
}
