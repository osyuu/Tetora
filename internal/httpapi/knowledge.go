package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

// KnowledgeSearchResult represents a matched knowledge chunk.
type KnowledgeSearchResult struct {
	Filename  string  `json:"filename"`
	Snippet   string  `json:"snippet"`
	Score     float64 `json:"score"`
	LineStart int     `json:"lineStart"`
}

// ReflectionResult holds a reflection output.
type ReflectionResult struct {
	TaskID      string  `json:"taskId"`
	Agent       string  `json:"agent"`
	Score       int     `json:"score"`
	Feedback    string  `json:"feedback"`
	Improvement string  `json:"improvement"`
	CostUSD     float64 `json:"costUsd"`
	CreatedAt   string  `json:"createdAt"`
}

// KnowledgeDeps holds dependencies for knowledge/reflection HTTP handlers.
type KnowledgeDeps struct {
	KnowledgeDir func() string
	HistoryDB    func() string
	// SearchKnowledge searches the knowledge directory. Returns results.
	SearchKnowledge func(dir, query string, limit int) ([]KnowledgeSearchResult, error)
	// QueryReflections queries reflections from history DB.
	QueryReflections func(dbPath, role string, limit int) ([]ReflectionResult, error)
}

// RegisterKnowledgeRoutes registers HTTP routes for knowledge search and reflections.
func RegisterKnowledgeRoutes(mux *http.ServeMux, deps KnowledgeDeps) {
	h := &knowledgeHandler{deps: deps}
	mux.HandleFunc("/knowledge/search", h.handleSearch)
	mux.HandleFunc("/reflections", h.handleReflections)
}

type knowledgeHandler struct {
	deps KnowledgeDeps
}

func (h *knowledgeHandler) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	q := r.URL.Query().Get("q")
	if q == "" {
		json.NewEncoder(w).Encode([]KnowledgeSearchResult{})
		return
	}
	limit := 10
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	results, err := h.deps.SearchKnowledge(h.deps.KnowledgeDir(), q, limit)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
		return
	}
	if results == nil {
		results = []KnowledgeSearchResult{}
	}
	json.NewEncoder(w).Encode(results)
}

func (h *knowledgeHandler) handleReflections(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"GET only"}`, http.StatusMethodNotAllowed)
		return
	}
	db := h.deps.HistoryDB()
	if db == "" {
		http.Error(w, `{"error":"history DB not configured"}`, http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	role := r.URL.Query().Get("role")
	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	refs, err := h.deps.QueryReflections(db, role, limit)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
		return
	}
	if refs == nil {
		refs = []ReflectionResult{}
	}
	json.NewEncoder(w).Encode(refs)
}
