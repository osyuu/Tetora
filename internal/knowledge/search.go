package knowledge

import (
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"
)

// SearchResult represents a matched knowledge chunk.
type SearchResult struct {
	Filename  string  `json:"filename"`
	Snippet   string  `json:"snippet"`
	Score     float64 `json:"score"`
	LineStart int     `json:"lineStart"`
}

// Index is an in-memory TF-IDF index.
type Index struct {
	mu        sync.RWMutex
	docs      map[string]*docEntry
	idf       map[string]float64
	totalDocs int
}

type docEntry struct {
	filename string
	lines    []string
	tf       map[string]float64
	size     int64
}

// BuildIndex scans all files in dir and builds a TF-IDF index.
func BuildIndex(dir string) (*Index, error) {
	idx := &Index{
		docs: make(map[string]*docEntry),
		idf:  make(map[string]float64),
	}
	if err := idx.rebuild(dir); err != nil {
		return nil, err
	}
	return idx, nil
}

// Rebuild re-scans the directory and rebuilds the index in place.
func (idx *Index) Rebuild(dir string) error {
	return idx.rebuild(dir)
}

// TotalDocs returns the number of documents in the index.
func (idx *Index) TotalDocs() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.totalDocs
}

// HasDoc reports whether a document with the given filename is indexed.
func (idx *Index) HasDoc(name string) bool {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	_, ok := idx.docs[name]
	return ok
}

// IDFLen returns the number of IDF entries (unique terms) in the index.
func (idx *Index) IDFLen() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.idf)
}

func (idx *Index) rebuild(dir string) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			idx.docs = make(map[string]*docEntry)
			idx.idf = make(map[string]float64)
			idx.totalDocs = 0
			return nil
		}
		return err
	}

	docs := make(map[string]*docEntry)
	df := make(map[string]int)

	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}

		info, err := e.Info()
		if err != nil {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}

		content := string(data)
		lines := strings.Split(content, "\n")
		tokens := Tokenize(content)

		termCounts := make(map[string]int)
		for _, tok := range tokens {
			termCounts[tok]++
		}
		total := len(tokens)
		tf := make(map[string]float64)
		if total > 0 {
			for term, count := range termCounts {
				tf[term] = float64(count) / float64(total)
			}
		}

		seen := make(map[string]bool)
		for _, tok := range tokens {
			if !seen[tok] {
				df[tok]++
				seen[tok] = true
			}
		}

		docs[e.Name()] = &docEntry{
			filename: e.Name(),
			lines:    lines,
			tf:       tf,
			size:     info.Size(),
		}
	}

	totalDocs := len(docs)

	idf := make(map[string]float64)
	for term, docCount := range df {
		idf[term] = math.Log(1.0 + float64(totalDocs)/float64(1+docCount))
	}

	idx.docs = docs
	idx.idf = idf
	idx.totalDocs = totalDocs
	return nil
}

// Search returns documents ranked by TF-IDF score for the given query.
func (idx *Index) Search(query string, maxResults int) []SearchResult {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	queryTokens := Tokenize(query)
	if len(queryTokens) == 0 {
		return nil
	}

	type scored struct {
		filename  string
		score     float64
		matchLine int
	}

	var results []scored

	for _, doc := range idx.docs {
		var score float64
		for _, qt := range queryTokens {
			tf, ok := doc.tf[qt]
			if !ok {
				continue
			}
			idf := idx.idf[qt]
			score += tf * idf
		}
		if score <= 0 {
			continue
		}

		bestLine := FindBestMatchLine(doc.lines, queryTokens)

		results = append(results, scored{
			filename:  doc.filename,
			score:     score,
			matchLine: bestLine,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	if maxResults > 0 && len(results) > maxResults {
		results = results[:maxResults]
	}

	var out []SearchResult
	for _, r := range results {
		doc := idx.docs[r.filename]
		snippet := BuildSnippet(doc.lines, r.matchLine, 1)
		out = append(out, SearchResult{
			Filename:  r.filename,
			Snippet:   snippet,
			Score:     r.score,
			LineStart: r.matchLine + 1,
		})
	}
	return out
}

// Tokenize splits text into lowercase Latin words and CJK unigrams/bigrams.
func Tokenize(text string) []string {
	var tokens []string

	var word []rune
	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if isCJK(r) {
			if len(word) > 0 {
				tokens = append(tokens, strings.ToLower(string(word)))
				word = word[:0]
			}
			tokens = append(tokens, string(r))
			if i+1 < len(runes) && isCJK(runes[i+1]) {
				tokens = append(tokens, string(runes[i])+string(runes[i+1]))
			}
		} else if unicode.IsLetter(r) || unicode.IsDigit(r) {
			word = append(word, unicode.ToLower(r))
		} else {
			if len(word) > 0 {
				tokens = append(tokens, string(word))
				word = word[:0]
			}
		}
	}
	if len(word) > 0 {
		tokens = append(tokens, string(word))
	}
	return tokens
}

func isCJK(r rune) bool {
	return IsCJK(r)
}

// IsCJK returns true if the rune is a CJK character (Unified Ideographs, Hiragana, Katakana).
func IsCJK(r rune) bool {
	return (r >= '\u4e00' && r <= '\u9fff') ||
		(r >= '\u3040' && r <= '\u309f') ||
		(r >= '\u30a0' && r <= '\u30ff')
}

// FindBestMatchLine returns the index of the line with the most query token hits.
func FindBestMatchLine(lines []string, queryTokens []string) int {
	bestLine := 0
	bestHits := 0

	for i, line := range lines {
		lineTokens := Tokenize(line)
		lineSet := make(map[string]bool)
		for _, lt := range lineTokens {
			lineSet[lt] = true
		}
		hits := 0
		for _, qt := range queryTokens {
			if lineSet[qt] {
				hits++
			}
		}
		if hits > bestHits {
			bestHits = hits
			bestLine = i
		}
	}
	return bestLine
}

// BuildSnippet returns a text snippet around matchLine with contextLines of context.
func BuildSnippet(lines []string, matchLine, contextLines int) string {
	if len(lines) == 0 {
		return ""
	}
	if matchLine < 0 {
		matchLine = 0
	}
	if matchLine >= len(lines) {
		matchLine = len(lines) - 1
	}

	start := matchLine - contextLines
	if start < 0 {
		start = 0
	}
	end := matchLine + contextLines + 1
	if end > len(lines) {
		end = len(lines)
	}

	selected := lines[start:end]
	snippet := strings.Join(selected, "\n")

	const maxSnippetLen = 200
	if len(snippet) > maxSnippetLen {
		snippet = snippet[:maxSnippetLen] + "..."
	}
	return snippet
}
