// Package audio provides PCM audio capture and playback.
package audio

// Recorder captures audio from the default microphone device.
// It returns a channel of float32 PCM samples at 16kHz mono.
type Recorder interface {
	// Start begins recording and returns a channel that emits audio
	// chunks. Each chunk is a slice of float32 samples normalized in
	// [-1, 1]. The channel is closed when recording stops.
	Start() (<-chan []float32, error)

	// Stop stops recording and closes the samples channel.
	Stop()
}