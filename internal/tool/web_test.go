package tool

import (
	"testing"
)

func TestURLEncode(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello world", "hello+world"},
		{"foo&bar", "foo%26bar"},
		{"key=value", "key%3Dvalue"},
		{"what?how", "what%3Fhow"},
		{"anchor#link", "anchor%23link"},
		{"simple", "simple"},
	}

	for _, tt := range tests {
		got := URLEncode(tt.input)
		if got != tt.want {
			t.Errorf("URLEncode(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
