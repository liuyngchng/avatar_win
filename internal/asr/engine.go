//go:build offline

// Package asr wraps the sherpa-onnx offline ASR engine for
// SenseVoiceSmall speech recognition.
package asr

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
)

// Engine wraps the sherpa-onnx offline ASR engine (SenseVoiceSmall).
type Engine struct {
	recognizer *sherpa.OfflineRecognizer
	sampleRate int
}

// modelPaths holds the required model file paths for SenseVoiceSmall.
type modelPaths struct {
	Model  string // model.int8.onnx
	Tokens string // tokens.txt
}

// NewOfflineEngine creates a new ASR engine with the given model directory.
// If modelDir is empty, the default ModelsDir() is used.
func NewOfflineEngine(modelDir string) (Transcriber, error) {
	if modelDir == "" {
		modelDir = ModelsDir()
	}

	p := modelPaths{
		Model:  filepath.Join(modelDir, "model.int8.onnx"),
		Tokens: filepath.Join(modelDir, "tokens.txt"),
	}

	for _, f := range []string{p.Model, p.Tokens} {
		if _, err := os.Stat(f); err != nil {
			return nil, fmt.Errorf("asr: model file not found: %s", f)
		}
	}

	numThreads := runtime.NumCPU()
	if numThreads > 4 {
		numThreads = 4
	}
	if numThreads < 2 {
		numThreads = 2
	}

	config := &sherpa.OfflineRecognizerConfig{
		FeatConfig: sherpa.FeatureConfig{
			SampleRate: 16000,
			FeatureDim: 80,
		},
		ModelConfig: sherpa.OfflineModelConfig{
			SenseVoice: sherpa.OfflineSenseVoiceModelConfig{
				Model:                       p.Model,
				Language:                    "auto",
				UseInverseTextNormalization: 1,
			},
			Tokens:     p.Tokens,
			NumThreads: numThreads,
			Provider:   "cpu",
			Debug:      0,
		},
		DecodingMethod: "greedy_search",
	}

	recognizer := sherpa.NewOfflineRecognizer(config)
	if recognizer == nil {
		return nil, fmt.Errorf("asr: failed to create recognizer (check model paths)")
	}

	slog.Info("engine_NewOfflineEngine_asr:_offline_engine_created", "num_threads", numThreads)

	return &Engine{
		recognizer: recognizer,
		sampleRate: 16000,
	}, nil
}

// Transcribe runs recognition on the provided PCM float32 samples.
// samples must be 16kHz mono, normalized in [-1, 1].
func (e *Engine) Transcribe(samples []float32, sampleRate int) (string, error) {
	if e.recognizer == nil {
		return "", fmt.Errorf("asr: engine not initialized")
	}
	if len(samples) == 0 {
		return "", fmt.Errorf("asr: no samples provided")
	}

	stream := sherpa.NewOfflineStream(e.recognizer)
	if stream == nil {
		return "", fmt.Errorf("asr: failed to create offline stream")
	}
	defer sherpa.DeleteOfflineStream(stream)

	stream.AcceptWaveform(e.sampleRate, samples)
	e.recognizer.Decode(stream)

	r := stream.GetResult()
	if r == nil {
		return "", fmt.Errorf("asr: no result")
	}

	slog.Debug("engine_Transcribe_asr:_offline_decoded", "samples", len(samples), "text", r.Text, "lang", r.Lang, "emotion", r.Emotion)

	return r.Text, nil
}

// Close releases the engine.
func (e *Engine) Close() {
	if e.recognizer != nil {
		sherpa.DeleteOfflineRecognizer(e.recognizer)
		e.recognizer = nil
	}
}

// ModelsDir returns the default path to ASR models.
func ModelsDir() string {
	candidates := []string{
		"models/asr",
		"../models/asr",
		filepath.Join(filepath.Dir(os.Args[0]), "models/asr"),
	}
	for _, d := range candidates {
		if _, err := os.Stat(filepath.Join(d, "model.int8.onnx")); err == nil {
			return d
		}
	}
	return "models/asr"
}
