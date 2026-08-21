// Package audio provides PCM audio capture and playback.
package audio

// Recorder captures audio from the default microphone device.
// It returns a channel of float32 PCM samples at 16kHz mono.
type Recorder interface {
	// Start begins recording and returns a channel that emits audio
	// chunks. Each chunk is a slice of float32 samples normalized in
	// [-1, 1]. The underlying WASAPI session is kept alive across
	// calls; only the first call pays the full initialization cost.
	Start() (<-chan []float32, error)

	// Stop permanently stops recording and releases the WASAPI session.
	// Call this only on application shutdown.
	Stop()
}