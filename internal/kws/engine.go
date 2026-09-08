// Package kws wraps the sherpa-onnx keyword spotting engine for
// wake word detection ("小然小然").
package kws

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
	"github.com/mozillazg/go-pinyin"
)

// DefaultWakeWord is the fallback wake word when none is configured.
// Uses the auto-generated format for the default name "小然".
const DefaultWakeWord = "x iǎo r án x iǎo r án @小然小然"

// Engine wraps the sherpa-onnx KeywordSpotter for wake word detection.
type Engine struct {
	spotter  *sherpa.KeywordSpotter
	keyword  string
	modelDir string
	stream   *sherpa.OnlineStream // internal stream for continuous detection
}

// New creates a new KWS engine with the given model directory and wake word.
// The wakeWord should be in sherpa keyword format: "phonemes @DisplayName"
// If empty, DefaultWakeWord is used.
func New(modelDir string, wakeWord string) (*Engine, error) {
	if wakeWord == "" {
		wakeWord = DefaultWakeWord
	}

	// Find model files by keyword (same approach as iOS/Android).
	encoder := findFile(modelDir, "encoder", ".onnx")
	decoder := findFile(modelDir, "decoder", ".onnx")
	joiner := findFile(modelDir, "joiner", ".onnx")
	tokens := findFile(modelDir, "tokens", ".txt")

	for _, f := range []string{encoder, decoder, joiner, tokens} {
		if f == "" {
			return nil, fmt.Errorf("kws: missing model file in %s", modelDir)
		}
	}

	slog.Info("kws_encoder_file", "path", encoder)
	slog.Info("kws_decoder_file", "path", decoder)
	slog.Info("kws_joiner_file", "path", joiner)
	slog.Info("kws_tokens_file", "path", tokens)

	numThreads := 1 // keep low, runs concurrently with ASR/TTS.

	config := &sherpa.KeywordSpotterConfig{
		FeatConfig: sherpa.FeatureConfig{
			SampleRate: 16000,
			FeatureDim: 80,
		},
		ModelConfig: sherpa.OnlineModelConfig{
			Transducer: sherpa.OnlineTransducerModelConfig{
				Encoder: encoder,
				Decoder: decoder,
				Joiner:  joiner,
			},
			Tokens:     tokens,
			NumThreads: numThreads,
			Provider:   "cpu",
			Debug:      0,
		},
		MaxActivePaths:    2,
		KeywordsScore:     6.0,
		KeywordsThreshold: 0.05,
		KeywordsBuf:       wakeWord,
		KeywordsBufSize:   len([]rune(wakeWord)), // character count, not byte count
	}

	spotter := sherpa.NewKeywordSpotter(config)
	if spotter == nil {
		return nil, fmt.Errorf("kws: failed to create keyword spotter")
	}

	stream := sherpa.NewKeywordStream(spotter)
	if stream == nil {
		sherpa.DeleteKeywordSpotter(spotter)
		return nil, fmt.Errorf("kws: failed to create keyword stream")
	}

	slog.Info("kws_engine_created", "keywords", wakeWord, "num_threads", numThreads)

	return &Engine{
		spotter:  spotter,
		keyword:  wakeWord,
		modelDir: modelDir,
		stream:   stream,
	}, nil
}

// ProcessSamples feeds audio samples to the keyword spotter and returns
// the detected keyword (empty string if no detection).
func (e *Engine) ProcessSamples(samples []float32) string {
	if e.stream == nil {
		return ""
	}
	e.stream.AcceptWaveform(16000, samples)
	for e.spotter.IsReady(e.stream) {
		e.spotter.Decode(e.stream)
		result := e.spotter.GetResult(e.stream)
		if result.Keyword != "" {
			e.spotter.Reset(e.stream)
			return result.Keyword
		}
	}
	return ""
}

// Close releases the engine.
func (e *Engine) Close() {
	if e.stream != nil {
		sherpa.DeleteOnlineStream(e.stream)
		e.stream = nil
	}
	if e.spotter != nil {
		sherpa.DeleteKeywordSpotter(e.spotter)
		e.spotter = nil
	}
}

// ModelsDir returns the default path to KWS models.
func ModelsDir() string {
	candidates := []string{
		"models/kws",
		"../models/kws",
		filepath.Join(filepath.Dir(os.Args[0]), "models/kws"),
	}
	for _, d := range candidates {
		if _, err := os.Stat(d); err == nil {
			return d
		}
	}
	return "models/kws"
}

// findFile searches a directory for a file whose name contains the keyword
// and ends with the given suffix. This handles model files with epoch/avg
// suffixes in their names.
func findFile(dir, keyword, suffix string) string {
	// Try the standard name first: keyword + suffix (e.g. "encoder.onnx").
	stdName := filepath.Join(dir, keyword+suffix)
	if _, err := os.Stat(stdName); err == nil {
		return stdName
	}

	// Fallback: scan directory for a file containing the keyword.
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.Contains(name, keyword) && strings.HasSuffix(name, suffix) {
			return filepath.Join(dir, name)
		}
	}
	return ""
}

// GenerateWakeWord generates a sherpa-onnx wake word string from a Chinese name.
// The format is: "initial final initial final ... @name+name" (name repeated twice).
// The initial and final are separated by spaces, with tone marks preserved on the
// final (e.g. "x iǎo r án @小然小然").
//
// If the name is empty, returns an empty string.
// If the name contains non-Chinese characters, they are skipped.
func GenerateWakeWord(name string) string {
	if name == "" {
		return ""
	}

	a := pinyin.NewArgs()
	a.Style = pinyin.Tone

	displayParts := make([]string, 0, len([]rune(name))*2)
	phoneParts := make([]string, 0, len([]rune(name))*2)

	for _, r := range name {
		if !unicode.Is(unicode.Han, r) {
			continue
		}
		displayParts = append(displayParts, string(r))

		py := pinyin.SinglePinyin(r, a)
		if len(py) == 0 {
			continue
		}

		initial, final := splitPinyinTone(py[0])
		if initial != "" {
			phoneParts = append(phoneParts, initial)
		}
		if final != "" {
			phoneParts = append(phoneParts, final)
		}
	}

	if len(phoneParts) == 0 || len(displayParts) == 0 {
		return ""
	}

	// Repeat the phone sequence twice (wake word = name repeated twice).
	phoneSeq := strings.Join(
		append(phoneParts, phoneParts...), " ")

	// Display name = name repeated twice.
	displayName := strings.Join(displayParts, "") + strings.Join(displayParts, "")

	return phoneSeq + " @" + displayName
}

// splitPinyinTone splits a pinyin syllable with tone into initial (声母) and
// final with tone (韵母+声调). e.g. "xiǎo" → ("x", "iǎo"), "rán" → ("r", "án").
// This preserves tone marks on the final, unlike the viseme splitter which
// strips them.
func splitPinyinTone(py string) (initial, final string) {
	// Multi-char initials: zh, ch, sh.
	if len(py) >= 2 && (py[:2] == "zh" || py[:2] == "ch" || py[:2] == "sh") {
		return py[:2], py[2:]
	}

	// Single-char initials.
	singleInitials := []string{"b", "p", "m", "f", "d", "t", "n", "l",
		"g", "k", "h", "j", "q", "x", "r", "z", "c", "s", "y", "w"}
	for _, ini := range singleInitials {
		if strings.HasPrefix(py, ini) {
			return ini, py[len(ini):]
		}
	}

	// No initial (e.g. "ài", "ǎo", "ǒu").
	return "", py
}
