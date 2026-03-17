package main

import (
	"strings"
	"testing"

	"tetora/internal/classify"
)

// --- String method ---

func TestRequestComplexityString(t *testing.T) {
	tests := []struct {
		c    classify.Complexity
		want string
	}{
		{classify.Simple, "simple"},
		{classify.Standard, "standard"},
		{classify.Complex, "complex"},
		{classify.Complexity(99), "standard"}, // unknown falls back
	}
	for _, tt := range tests {
		if got := tt.c.String(); got != tt.want {
			t.Errorf("classify.Complexity(%d).String() = %q, want %q", int(tt.c), got, tt.want)
		}
	}
}

// --- Simple greeting detection ---

func TestClassifySimpleGreeting(t *testing.T) {
	cases := []struct {
		prompt string
		source string
	}{
		{"hello", "discord"},
		{"hi there!", "telegram"},
		{"おはよう", "line"},
		{"hey", "slack"},
		{"good morning", "whatsapp"},
		{"yo", "matrix"},
		{"sup", "teams"},
		{"hi", "signal"},
		{"hey there", "gchat"},
		{"hello!", "imessage"},
		{"How are you?", "chat"},
	}
	for _, tc := range cases {
		got := classify.Classify(tc.prompt, tc.source)
		if got != classify.Simple {
			t.Errorf("classify.Classify(%q, %q) = %v, want simple", tc.prompt, tc.source, got)
		}
	}
}

// --- Complex code task detection ---

func TestClassifyComplexCodingKeywords(t *testing.T) {
	cases := []struct {
		prompt string
	}{
		{"Please implement a new feature"},
		{"Debug the login endpoint"},
		{"Refactor the database schema"},
		{"Build an API for user management"},
		{"Write a function to sort the list"},
		{"Deploy the application to production"},
		{"Optimize the algorithm for speed"},
		{"Fix the SQL query performance"},
		{"Set up authentication and authorization"},
		{"Create a migration for the new table"},
		{"CODE review this pull request"},          // case-insensitive
		{"The DATABASE needs a new index"},         // case-insensitive
		{"please compile this project"},            // lowercase keyword
		{"Run the benchmark for concurrency test"}, // multiple keywords
	}
	for _, tc := range cases {
		got := classify.Classify(tc.prompt, "discord")
		if got != classify.Complex {
			t.Errorf("classify.Classify(%q, discord) = %v, want complex", tc.prompt, got)
		}
	}
}

func TestClassifyComplexJapaneseKeywords(t *testing.T) {
	cases := []struct {
		prompt string
	}{
		{"この関数を実装してください"},
		{"デバッグをお願いします"},
		{"リファクタリングが必要です"},  // contains リファクタ
		{"データベースのスキーマを更新して"},
		{"アルゴリズムを最適化して"},
		{"認証の仕組みを設計して"},
		{"コードレビューして"},
		{"パイプラインを構築して"},
	}
	for _, tc := range cases {
		got := classify.Classify(tc.prompt, "discord")
		if got != classify.Complex {
			t.Errorf("classify.Classify(%q, discord) = %v, want complex", tc.prompt, got)
		}
	}
}

// --- Standard middle-ground ---

func TestClassifyStandard(t *testing.T) {
	cases := []struct {
		prompt string
		source string
	}{
		// > 100 runes from a chat source, no coding keywords → standard
		{"Tell me about the weather in Tokyo tomorrow and what I should wear for the upcoming outdoor occasion this weekend", "discord"},
		{"Can you summarize the latest news about climate change?", "http"},
		{"What is the capital of France?", "http"},
		// Long-ish prompt from chat source but no keywords, > 100 runes
		{"I was wondering if you could help me understand the general process of how things work around here in more detail please", "discord"},
	}
	for _, tc := range cases {
		got := classify.Classify(tc.prompt, tc.source)
		if got != classify.Standard {
			t.Errorf("classify.Classify(%q, %q) = %v, want standard", tc.prompt, tc.source, got)
		}
	}
}

// --- CJK character length handling ---

func TestClassifyCJKLength(t *testing.T) {
	// 99 CJK characters should be < 100 rune threshold → simple from chat source
	short := strings.Repeat("あ", 99)
	if got := classify.Classify(short, "discord"); got != classify.Simple {
		t.Errorf("99 CJK runes from discord = %v, want simple", got)
	}

	// 100 CJK characters should be >= 100 → standard (no keywords, chat source)
	exact100 := strings.Repeat("あ", 100)
	if got := classify.Classify(exact100, "discord"); got != classify.Standard {
		t.Errorf("100 CJK runes from discord = %v, want standard", got)
	}

	// 2001 CJK characters should be > 2000 → complex
	long := strings.Repeat("漢", 2001)
	if got := classify.Classify(long, "discord"); got != classify.Complex {
		t.Errorf("2001 CJK runes from discord = %v, want complex", got)
	}
}

// --- Source-based overrides ---

func TestClassifySourceCronKeywordBased(t *testing.T) {
	// Short cron prompt → Simple (keyword-based, not auto-Complex).
	got := classify.Classify("hello", "cron")
	if got != classify.Simple {
		t.Errorf("classify.Classify(hello, cron) = %v, want simple", got)
	}
}

func TestClassifySourceWorkflowKeywordBased(t *testing.T) {
	// Short workflow prompt → Simple (keyword-based, not auto-Complex).
	got := classify.Classify("check status", "workflow")
	if got != classify.Simple {
		t.Errorf("classify.Classify(check status, workflow) = %v, want simple", got)
	}
}

func TestClassifySourceOverrideAgentComm(t *testing.T) {
	got := classify.Classify("ping", "agent-comm")
	if got != classify.Complex {
		t.Errorf("classify.Classify(ping, agent-comm) = %v, want complex", got)
	}
}

func TestClassifySourceCaseInsensitive(t *testing.T) {
	// Source matching should be case-insensitive.
	got := classify.Classify("hi", "Discord")
	if got != classify.Simple {
		t.Errorf("classify.Classify(hi, Discord) = %v, want simple", got)
	}

	// Short cron prompt → Simple (keyword-based, not auto-Complex).
	got2 := classify.Classify("hi", "CRON")
	if got2 != classify.Simple {
		t.Errorf("classify.Classify(hi, CRON) = %v, want simple", got2)
	}
}

// --- Edge cases ---

func TestClassifyEmptyString(t *testing.T) {
	// Empty prompt from chat source: length 0 < 100 → simple
	got := classify.Classify("", "discord")
	if got != classify.Simple {
		t.Errorf("classify.Classify(empty, discord) = %v, want simple", got)
	}

	// Empty prompt from unknown source: no keywords, length 0 < 100, but source not in ChatSources
	got2 := classify.Classify("", "http")
	if got2 != classify.Standard {
		t.Errorf("classify.Classify(empty, http) = %v, want standard", got2)
	}

	// Empty prompt and empty source
	got3 := classify.Classify("", "")
	if got3 != classify.Standard {
		t.Errorf("classify.Classify(empty, empty) = %v, want standard", got3)
	}
}

func TestClassifyExactly100Chars(t *testing.T) {
	// Exactly 100 ASCII characters, no keywords, chat source → standard (not simple; threshold is < 100)
	prompt := strings.Repeat("a", 100)
	got := classify.Classify(prompt, "discord")
	if got != classify.Standard {
		t.Errorf("100 ascii chars from discord = %v, want standard", got)
	}

	// 99 ASCII characters, no keywords, chat source → simple
	prompt99 := strings.Repeat("a", 99)
	got2 := classify.Classify(prompt99, "discord")
	if got2 != classify.Simple {
		t.Errorf("99 ascii chars from discord = %v, want simple", got2)
	}
}

func TestClassifyExactly2000Chars(t *testing.T) {
	// Exactly 2000 characters → not > 2000, so not auto-complex
	prompt := strings.Repeat("x", 2000)
	got := classify.Classify(prompt, "discord")
	if got != classify.Standard {
		t.Errorf("2000 chars from discord = %v, want standard", got)
	}

	// 2001 characters → complex
	prompt2001 := strings.Repeat("x", 2001)
	got2 := classify.Classify(prompt2001, "discord")
	if got2 != classify.Complex {
		t.Errorf("2001 chars from discord = %v, want complex", got2)
	}
}

// --- Helper functions ---

func TestComplexityMaxSessionMessages(t *testing.T) {
	tests := []struct {
		c    classify.Complexity
		want int
	}{
		{classify.Simple, 5},
		{classify.Standard, 10},
		{classify.Complex, 20},
		{classify.Complexity(99), 10}, // unknown falls back
	}
	for _, tt := range tests {
		if got := classify.MaxSessionMessages(tt.c); got != tt.want {
			t.Errorf("classify.MaxSessionMessages(%v) = %d, want %d", tt.c, got, tt.want)
		}
	}
}

func TestComplexityMaxSessionChars(t *testing.T) {
	tests := []struct {
		c    classify.Complexity
		want int
	}{
		{classify.Simple, 4000},
		{classify.Standard, 8000},
		{classify.Complex, 16000},
		{classify.Complexity(99), 8000}, // unknown falls back
	}
	for _, tt := range tests {
		if got := classify.MaxSessionChars(tt.c); got != tt.want {
			t.Errorf("classify.MaxSessionChars(%v) = %d, want %d", tt.c, got, tt.want)
		}
	}
}

// --- Keyword case insensitivity ---

func TestClassifyKeywordCaseInsensitive(t *testing.T) {
	cases := []string{
		"Please IMPLEMENT this",
		"DEBUG the issue",
		"The Api is broken",
		"Fix the DATABASE",
		"SQL injection vulnerability",
		"ALGORITHM complexity",
	}
	for _, prompt := range cases {
		got := classify.Classify(prompt, "discord")
		if got != classify.Complex {
			t.Errorf("classify.Classify(%q, discord) = %v, want complex", prompt, got)
		}
	}
}

// --- Mixed scenarios ---

func TestClassifyShortWithKeyword(t *testing.T) {
	// Short prompt but contains a keyword → complex wins over simple.
	got := classify.Classify("fix the code", "discord")
	if got != classify.Complex {
		t.Errorf("classify.Classify(fix the code, discord) = %v, want complex", got)
	}
}

func TestClassifyLongFromChatNoKeywords(t *testing.T) {
	// >100 runes from a chat source, no keywords → standard.
	prompt := "I would really appreciate it if you could tell me what the weather forecast looks like for the next few days because I am planning a trip"
	got := classify.Classify(prompt, "discord")
	if got != classify.Standard {
		t.Errorf("classify.Classify(long no-keyword, discord) = %v, want standard", got)
	}
}
