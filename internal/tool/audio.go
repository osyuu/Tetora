package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// AudioNormalize normalizes audio volume using ffmpeg loudnorm filter.
func AudioNormalize(ctx context.Context, input json.RawMessage) (string, error) {
	var args struct {
		Path       string  `json:"path"`
		TargetLUFS float64 `json:"target_lufs"`
		Output     string  `json:"output"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	if args.TargetLUFS == 0 {
		args.TargetLUFS = -14
	}

	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return "", fmt.Errorf("ffmpeg not found in PATH — install it first")
	}

	if _, err := os.Stat(args.Path); err != nil {
		return "", fmt.Errorf("input file not found: %w", err)
	}

	outputPath := args.Output
	inPlace := false
	if outputPath == "" {
		tmpDir, err := os.MkdirTemp("", "tetora-audio-*")
		if err != nil {
			return "", fmt.Errorf("create temp dir: %w", err)
		}
		defer os.RemoveAll(tmpDir)
		outputPath = filepath.Join(tmpDir, filepath.Base(args.Path))
		inPlace = true
	}

	loudnormFilter := fmt.Sprintf("loudnorm=I=%.1f:TP=-1:LRA=11", args.TargetLUFS)
	cmdCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, ffmpegPath, "-i", args.Path,
		"-af", loudnormFilter, "-y", outputPath)

	var stderr strings.Builder
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errStr := stderr.String()
		if len(errStr) > 500 {
			errStr = errStr[:500] + "..."
		}
		return "", fmt.Errorf("ffmpeg failed: %w\nstderr: %s", err, errStr)
	}

	if inPlace {
		data, err := os.ReadFile(outputPath)
		if err != nil {
			return "", fmt.Errorf("read normalized file: %w", err)
		}
		if err := os.WriteFile(args.Path, data, 0o644); err != nil {
			return "", fmt.Errorf("write back: %w", err)
		}
		outputPath = args.Path
	}

	result := map[string]any{
		"original_path": args.Path,
		"output_path":   outputPath,
		"target_lufs":   args.TargetLUFS,
		"message":       fmt.Sprintf("normalized to %.1f LUFS", args.TargetLUFS),
	}
	b, _ := json.Marshal(result)
	return string(b), nil
}
