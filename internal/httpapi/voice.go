package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"tetora/internal/log"
)

// VoiceTranscribeOpts holds STT options for transcription.
type VoiceTranscribeOpts struct {
	Language string
	Format   string
}

// VoiceSynthesizeOpts holds TTS options for synthesis.
type VoiceSynthesizeOpts struct {
	Voice  string
	Speed  float64
	Format string
}

// VoiceDeps holds dependencies for voice HTTP handlers.
type VoiceDeps struct {
	STTEnabled       bool
	TTSEnabled       bool
	WakeEnabled      bool
	RealtimeEnabled  bool
	DefaultTTSFormat string
	Transcribe       func(ctx context.Context, audio io.Reader, opts VoiceTranscribeOpts) (any, error)
	Synthesize       func(ctx context.Context, text string, opts VoiceSynthesizeOpts) (io.ReadCloser, error)
	HandleWakeWS     http.HandlerFunc
	HandleRealtimeWS http.HandlerFunc
}

// RegisterVoiceRoutes registers voice API routes.
func RegisterVoiceRoutes(mux *http.ServeMux, d VoiceDeps) {
	mux.HandleFunc("/api/voice/transcribe", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		if !d.STTEnabled {
			http.Error(w, `{"error":"voice stt not enabled"}`, http.StatusServiceUnavailable)
			return
		}

		if err := r.ParseMultipartForm(32 << 20); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"parse form: %v"}`, err), http.StatusBadRequest)
			return
		}

		file, header, err := r.FormFile("audio")
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"missing audio field: %v"}`, err), http.StatusBadRequest)
			return
		}
		defer file.Close()

		language := r.FormValue("language")
		format := r.FormValue("format")
		if format == "" {
			if strings.HasSuffix(header.Filename, ".ogg") {
				format = "ogg"
			} else if strings.HasSuffix(header.Filename, ".wav") {
				format = "wav"
			} else if strings.HasSuffix(header.Filename, ".webm") {
				format = "webm"
			} else {
				format = "mp3"
			}
		}

		result, err := d.Transcribe(r.Context(), file, VoiceTranscribeOpts{
			Language: language,
			Format:   format,
		})
		if err != nil {
			log.ErrorCtx(r.Context(), "voice transcribe failed", "error", err)
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	})

	mux.HandleFunc("/api/voice/synthesize", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
			return
		}
		if !d.TTSEnabled {
			http.Error(w, `{"error":"voice tts not enabled"}`, http.StatusServiceUnavailable)
			return
		}

		var req struct {
			Text   string  `json:"text"`
			Voice  string  `json:"voice,omitempty"`
			Speed  float64 `json:"speed,omitempty"`
			Format string  `json:"format,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"invalid json: %v"}`, err), http.StatusBadRequest)
			return
		}
		if req.Text == "" {
			http.Error(w, `{"error":"text field required"}`, http.StatusBadRequest)
			return
		}

		stream, err := d.Synthesize(r.Context(), req.Text, VoiceSynthesizeOpts{
			Voice:  req.Voice,
			Speed:  req.Speed,
			Format: req.Format,
		})
		if err != nil {
			log.ErrorCtx(r.Context(), "voice synthesize failed", "error", err)
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}
		defer stream.Close()

		format := req.Format
		if format == "" {
			format = d.DefaultTTSFormat
		}
		if format == "" {
			format = "mp3"
		}
		contentType := "audio/mpeg"
		if format == "opus" {
			contentType = "audio/opus"
		} else if format == "wav" {
			contentType = "audio/wav"
		}

		w.Header().Set("Content-Type", contentType)
		io.Copy(w, stream)
	})

	mux.HandleFunc("/ws/voice/wake", func(w http.ResponseWriter, r *http.Request) {
		if !d.WakeEnabled {
			http.Error(w, `{"error":"voice wake not enabled"}`, http.StatusServiceUnavailable)
			return
		}
		if d.HandleWakeWS == nil {
			http.Error(w, `{"error":"voice realtime engine not initialized"}`, http.StatusServiceUnavailable)
			return
		}
		d.HandleWakeWS(w, r)
	})

	mux.HandleFunc("/ws/voice/realtime", func(w http.ResponseWriter, r *http.Request) {
		if !d.RealtimeEnabled {
			http.Error(w, `{"error":"voice realtime not enabled"}`, http.StatusServiceUnavailable)
			return
		}
		if d.HandleRealtimeWS == nil {
			http.Error(w, `{"error":"voice realtime engine not initialized"}`, http.StatusServiceUnavailable)
			return
		}
		d.HandleRealtimeWS(w, r)
	})
}
