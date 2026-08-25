// Package tts provides text-to-speech. Text can be synthesized either via
// the Alibaba Cloud DashScope Qwen-TTS Realtime WebSocket API (Client,
// online) or via a local sherpa-onnx Matcha-TTS model (Engine, offline).
// Both implementations satisfy the Synthesizer interface so the brain can
// switch between them based on cfg.yml.
package tts

// SynthesizeResult contains the result of a TTS synthesis.
type SynthesizeResult struct {
	// Samples are normalized float32 PCM samples in [-1, 1].
	Samples []float32
	// SampleRate is the output sample rate in Hz.
	SampleRate int
	// Duration is the audio duration in seconds.
	Duration float64
}

// Synthesizer converts text into speech audio. Implementations may be online
// (network API) or offline (local model), but expose the same methods.
type Synthesizer interface {
	// Synthesize converts text to speech. speed is a playback-rate multiplier
	// (1.0 = normal).
	Synthesize(text string, speed float32) (*SynthesizeResult, error)

	// SampleRate returns the output sample rate (e.g. 24000 or 22050).
	SampleRate() int

	// Close releases any underlying resources (connections, models, etc.).
	Close()
}