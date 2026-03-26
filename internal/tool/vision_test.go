package tool

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func makePNGBytes() []byte {
	return []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xde,
		0x00, 0x00, 0x00, 0x0c, 0x49, 0x44, 0x41, 0x54,
		0x08, 0xd7, 0x63, 0xf8, 0xcf, 0xc0, 0x00, 0x00,
		0x00, 0x02, 0x00, 0x01, 0xe2, 0x21, 0xbc, 0x33,
		0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44,
		0xae, 0x42, 0x60, 0x82,
	}
}

func makeJPEGBytes() []byte {
	return []byte{
		0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 0x4a, 0x46,
		0x49, 0x46, 0x00, 0x01, 0x01, 0x00, 0x00, 0x01,
		0x00, 0x01, 0x00, 0x00, 0xff, 0xd9,
	}
}

func TestDetectMediaType(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{"PNG", makePNGBytes(), "image/png"},
		{"JPEG", makeJPEGBytes(), "image/jpeg"},
		{"GIF87a", append([]byte("GIF87a"), make([]byte, 10)...), "image/gif"},
		{"GIF89a", append([]byte("GIF89a"), make([]byte, 10)...), "image/gif"},
		{"WebP", append(append([]byte("RIFF"), make([]byte, 4)...), []byte("WEBP")...), "image/webp"},
		{"unknown", []byte("not an image at all!"), ""},
		{"too short", []byte("abc"), ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectMediaType(tt.data)
			if got != tt.want {
				t.Errorf("DetectMediaType() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsBase64Image(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"data URI", "data:image/png;base64,iVBOR...", true},
		{"HTTP URL", "https://example.com/image.png", false},
		{"plain base64", strings.Repeat("ABCD", 30), true},
		{"short string", "abc", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsBase64Image(tt.input)
			if got != tt.want {
				t.Errorf("IsBase64Image(%q...) = %v, want %v", tt.input[:min(len(tt.input), 20)], got, tt.want)
			}
		})
	}
}

func TestParseBase64Image(t *testing.T) {
	pngData := makePNGBytes()
	b64 := base64.StdEncoding.EncodeToString(pngData)
	dataURI := "data:image/png;base64," + b64

	data, mediaType, err := ParseBase64Image(dataURI)
	if err != nil {
		t.Fatalf("ParseBase64Image(data URI) error: %v", err)
	}
	if mediaType != "image/png" {
		t.Errorf("mediaType = %q, want image/png", mediaType)
	}
	if len(data) != len(pngData) {
		t.Errorf("data length = %d, want %d", len(data), len(pngData))
	}

	data2, mediaType2, err := ParseBase64Image(b64)
	if err != nil {
		t.Fatalf("ParseBase64Image(raw base64) error: %v", err)
	}
	if mediaType2 != "image/png" {
		t.Errorf("mediaType = %q, want image/png", mediaType2)
	}
	if len(data2) != len(pngData) {
		t.Errorf("data length = %d, want %d", len(data2), len(pngData))
	}

	_, _, err = ParseBase64Image("data:image/png;base64,!!!invalid!!!")
	if err == nil {
		t.Error("expected error for invalid base64")
	}

	_, _, err = ParseBase64Image("data:nope")
	if err == nil {
		t.Error("expected error for invalid data URI")
	}
}

func TestFetchImage(t *testing.T) {
	pngData := makePNGBytes()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(pngData)
	}))
	defer srv.Close()

	ctx := context.Background()
	data, mediaType, err := FetchImage(ctx, srv.URL+"/image.png", DefaultMaxImageSize)
	if err != nil {
		t.Fatalf("FetchImage error: %v", err)
	}
	if mediaType != "image/png" {
		t.Errorf("mediaType = %q, want image/png", mediaType)
	}
	if len(data) != len(pngData) {
		t.Errorf("data length = %d, want %d", len(data), len(pngData))
	}
}

func TestFetchImageOversize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(make([]byte, 1024))
	}))
	defer srv.Close()

	ctx := context.Background()
	_, _, err := FetchImage(ctx, srv.URL+"/large.jpg", 100)
	if err == nil {
		t.Error("expected error for oversize image")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("error = %q, want contains 'too large'", err.Error())
	}
}

func TestFetchImageUnsupportedFormat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("not an image at all, just plain text content here"))
	}))
	defer srv.Close()

	ctx := context.Background()
	_, _, err := FetchImage(ctx, srv.URL+"/text.txt", DefaultMaxImageSize)
	if err == nil {
		t.Error("expected error for unsupported format")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("error = %q, want contains 'unsupported'", err.Error())
	}
}

func TestAnthropicProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path = %q, want /v1/messages", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("x-api-key = %q, want test-key", r.Header.Get("x-api-key"))
		}

		var reqBody map[string]any
		json.NewDecoder(r.Body).Decode(&reqBody)
		messages := reqBody["messages"].([]any)
		msg := messages[0].(map[string]any)
		content := msg["content"].([]any)
		if len(content) != 2 {
			t.Errorf("expected 2 content blocks, got %d", len(content))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "A 1x1 white pixel image."},
			},
		})
	}))
	defer srv.Close()

	provider := &AnthropicVision{}
	cfg := &VisionConfig{APIKey: "test-key", Model: "claude-sonnet-4-5-20250929", BaseURL: srv.URL}
	ctx := context.Background()

	result, err := provider.Analyze(ctx, cfg, makePNGBytes(), "image/png", "Describe this", "auto")
	if err != nil {
		t.Fatalf("Analyze error: %v", err)
	}
	if !strings.Contains(result, "1x1") {
		t.Errorf("result = %q, want contains '1x1'", result)
	}
}

func TestOpenAIProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q, want /v1/chat/completions", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": "A small white pixel."}},
			},
		})
	}))
	defer srv.Close()

	provider := &OpenAIVision{}
	cfg := &VisionConfig{APIKey: "test-key", Model: "gpt-4o", BaseURL: srv.URL}
	ctx := context.Background()

	result, err := provider.Analyze(ctx, cfg, makePNGBytes(), "image/png", "Describe this", "high")
	if err != nil {
		t.Fatalf("Analyze error: %v", err)
	}
	if !strings.Contains(result, "pixel") {
		t.Errorf("result = %q, want contains 'pixel'", result)
	}
}

func TestGoogleProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "generateContent") {
			t.Errorf("path = %q, want contains 'generateContent'", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{
				{"content": map[string]any{
					"parts": []map[string]any{
						{"text": "A white pixel image."},
					},
				}},
			},
		})
	}))
	defer srv.Close()

	provider := &GoogleVision{}
	cfg := &VisionConfig{APIKey: "test-key", Model: "gemini-2.0-flash", BaseURL: srv.URL}
	ctx := context.Background()

	result, err := provider.Analyze(ctx, cfg, makePNGBytes(), "image/png", "Describe this", "auto")
	if err != nil {
		t.Fatalf("Analyze error: %v", err)
	}
	if !strings.Contains(result, "pixel") {
		t.Errorf("result = %q, want contains 'pixel'", result)
	}
}

func TestImageAnalyzeHandler(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "This is a test image."},
			},
		})
	}))
	defer srv.Close()

	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(makePNGBytes())
	}))
	defer imgSrv.Close()

	cfg := VisionConfig{Provider: "anthropic", APIKey: "test-key", BaseURL: srv.URL}
	ctx := context.Background()

	input := json.RawMessage(fmt.Sprintf(`{"image": "%s/photo.png", "prompt": "Describe this image"}`, imgSrv.URL))
	result, err := ImageAnalyze(ctx, cfg, input)
	if err != nil {
		t.Fatalf("ImageAnalyze error: %v", err)
	}
	if !strings.Contains(result, "test image") {
		t.Errorf("result = %q, want contains 'test image'", result)
	}

	b64 := base64.StdEncoding.EncodeToString(makePNGBytes())
	input = json.RawMessage(fmt.Sprintf(`{"image": "data:image/png;base64,%s", "prompt": "Describe"}`, b64))
	result, err = ImageAnalyze(ctx, cfg, input)
	if err != nil {
		t.Fatalf("ImageAnalyze base64 error: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty result for base64 input")
	}
}

func TestImageAnalyzeErrors(t *testing.T) {
	ctx := context.Background()
	cfg := VisionConfig{Provider: "anthropic", APIKey: "test"}

	_, err := ImageAnalyze(ctx, cfg, json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "image is required") {
		t.Errorf("expected 'image is required' error, got %v", err)
	}

	_, err = ImageAnalyze(ctx, cfg, json.RawMessage(`{"image": "https://x.com/a.png", "detail": "super"}`))
	if err == nil || !strings.Contains(err.Error(), "invalid detail") {
		t.Errorf("expected 'invalid detail' error, got %v", err)
	}

	cfg2 := VisionConfig{}
	_, err = ImageAnalyze(ctx, cfg2, json.RawMessage(`{"image": "https://x.com/a.png"}`))
	if err == nil || !strings.Contains(err.Error(), "vision not configured") {
		t.Errorf("expected 'vision not configured' error, got %v", err)
	}
}

func TestOversizeImageRejection(t *testing.T) {
	ctx := context.Background()
	cfg := VisionConfig{Provider: "anthropic", APIKey: "test-key", MaxImageSize: 10}

	pngData := makePNGBytes()
	b64 := base64.StdEncoding.EncodeToString(pngData)
	input := json.RawMessage(fmt.Sprintf(`{"image": "data:image/png;base64,%s"}`, b64))

	_, err := ImageAnalyze(ctx, cfg, input)
	if err == nil {
		t.Error("expected error for oversize image")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("error = %q, want contains 'too large'", err.Error())
	}
}

func TestResolveVisionProvider(t *testing.T) {
	tests := []struct {
		provider string
		wantNil  bool
	}{
		{"anthropic", false},
		{"openai", false},
		{"google", false},
		{"", true},
		{"unknown", true},
	}

	for _, tc := range tests {
		p := ResolveVisionProvider(VisionConfig{Provider: tc.provider})
		if tc.wantNil && p != nil {
			t.Errorf("ResolveVisionProvider(%q) = %v, want nil", tc.provider, p)
		}
		if !tc.wantNil && p == nil {
			t.Errorf("ResolveVisionProvider(%q) = nil, want non-nil", tc.provider)
		}
	}
}

func TestProviderAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "internal server error"}`))
	}))
	defer srv.Close()

	ctx := context.Background()
	providers := []struct {
		name     string
		provider visionProvider
	}{
		{"anthropic", &AnthropicVision{}},
		{"openai", &OpenAIVision{}},
		{"google", &GoogleVision{}},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			cfg := &VisionConfig{APIKey: "test-key", BaseURL: srv.URL}
			_, err := p.provider.Analyze(ctx, cfg, makePNGBytes(), "image/png", "test", "auto")
			if err == nil {
				t.Error("expected error for API error response")
			}
			if !strings.Contains(err.Error(), "500") {
				t.Errorf("error = %q, want contains '500'", err.Error())
			}
		})
	}
}

func TestProviderEmptyResponse(t *testing.T) {
	ctx := context.Background()

	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"content": []any{}})
	}))
	defer srv1.Close()
	p1 := &AnthropicVision{}
	_, err := p1.Analyze(ctx, &VisionConfig{APIKey: "k", BaseURL: srv1.URL}, makePNGBytes(), "image/png", "test", "auto")
	if err == nil || !strings.Contains(err.Error(), "empty response") {
		t.Errorf("anthropic: expected 'empty response' error, got %v", err)
	}

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{}})
	}))
	defer srv2.Close()
	p2 := &OpenAIVision{}
	_, err = p2.Analyze(ctx, &VisionConfig{APIKey: "k", BaseURL: srv2.URL}, makePNGBytes(), "image/png", "test", "auto")
	if err == nil || !strings.Contains(err.Error(), "empty response") {
		t.Errorf("openai: expected 'empty response' error, got %v", err)
	}

	srv3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"candidates": []any{}})
	}))
	defer srv3.Close()
	p3 := &GoogleVision{}
	_, err = p3.Analyze(ctx, &VisionConfig{APIKey: "k", BaseURL: srv3.URL}, makePNGBytes(), "image/png", "test", "auto")
	if err == nil || !strings.Contains(err.Error(), "empty response") {
		t.Errorf("google: expected 'empty response' error, got %v", err)
	}
}

func TestProviderMissingAPIKey(t *testing.T) {
	ctx := context.Background()
	providers := []struct {
		name     string
		provider visionProvider
	}{
		{"anthropic", &AnthropicVision{}},
		{"openai", &OpenAIVision{}},
		{"google", &GoogleVision{}},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			cfg := &VisionConfig{APIKey: ""}
			_, err := p.provider.Analyze(ctx, cfg, makePNGBytes(), "image/png", "test", "auto")
			if err == nil || !strings.Contains(err.Error(), "requires apiKey") {
				t.Errorf("expected 'requires apiKey' error, got %v", err)
			}
		})
	}
}
