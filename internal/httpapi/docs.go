package httpapi

import (
	"embed"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
)

// DocsPageEntry describes a documentation page.
type DocsPageEntry struct {
	Name        string   `json:"name"`
	File        string   `json:"file"`
	Description string   `json:"description"`
	Langs       []string `json:"langs"`
}

// RegisterDocsRoutes registers HTTP routes for serving embedded documentation.
// The fs must contain the doc files referenced in docsList.
func RegisterDocsRoutes(mux *http.ServeMux, fs embed.FS, docsList []DocsPageEntry, supportedLangs []string) {
	// Build allowed files set and populate langs.
	allowed := make(map[string]struct{}, len(docsList))
	for i := range docsList {
		entry := &docsList[i]
		allowed[entry.File] = struct{}{}
		// i18n variants live in docs/i18n/ using the base filename only (no directory prefix).
		// e.g. README.md → docs/i18n/README.zh-TW.md
		base := strings.TrimSuffix(filepath.Base(entry.File), ".md")
		var langs []string
		for _, lang := range supportedLangs {
			candidate := "docs/i18n/" + base + "." + lang + ".md"
			if _, err := fs.ReadFile(candidate); err == nil {
				langs = append(langs, lang)
				allowed[candidate] = struct{}{}
			}
		}
		entry.Langs = langs
	}

	// GET /api/docs — list available documentation files
	mux.HandleFunc("/api/docs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(docsList)
	})

	// GET /api/docs/{file}?lang=xx — return raw markdown content
	mux.HandleFunc("/api/docs/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
			return
		}

		filePath := strings.TrimPrefix(r.URL.Path, "/api/docs/")
		if filePath == "" {
			http.Error(w, `{"error":"file path required"}`, http.StatusBadRequest)
			return
		}

		if _, ok := allowed[filePath]; !ok {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}

		resolvedPath := filePath
		if lang := r.URL.Query().Get("lang"); lang != "" && lang != "en" {
			base := strings.TrimSuffix(filepath.Base(filePath), ".md")
			candidate := "docs/i18n/" + base + "." + lang + ".md"
			if _, ok := allowed[candidate]; ok {
				resolvedPath = candidate
			}
		}

		data, err := fs.ReadFile(resolvedPath)
		if err != nil {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write(data)
	})
}
