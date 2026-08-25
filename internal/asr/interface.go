// Package asr provides automatic speech recognition. Speech can be
// transcribed either via the Alibaba Cloud DashScope WebSocket API
// (Client, online) or via a local sherpa-onnx SenseVoiceSmall model
// (Engine, offline). Both implementations satisfy the Transcriber
// interface so the brain can switch between them based on cfg.yml.
package asr

// Transcriber converts recorded PCM audio into text. Implementations may be
// online (network API) or offline (local model), but expose the same method.
type Transcriber interface {
	// Transcribe converts float32 PCM samples (normalized in [-1, 1]) at the
	// given sample rate to text.
	Transcribe(samples []float32, sampleRate int) (string, error)

	// Close releases any underlying resources (connections, models, etc.).
	Close()
}
