package main

// export.go is a thin facade wrapping internal/export.
// Business logic lives in internal/export/; this file bridges globals and *Config.

import "tetora/internal/export"

// --- Type aliases ---

type ExportResult = export.Result

// --- Forwarding functions ---

func exportUserData(cfg *Config, userID string) (*ExportResult, error) {
	return export.UserData(cfg.HistoryDB, cfg.baseDir, userID)
}

func createZipFromDir(srcDir, destPath string) error {
	return export.ZipFromDir(srcDir, destPath)
}
