// Package tts provides text-to-speech via the Alibaba Cloud DashScope
// Qwen-TTS Realtime WebSocket API.
//
// Protocol: Qwen-TTS Realtime API (WebSocket).
//   - Connect to wss://dashscope.aliyuncs.com/api-ws/v1/realtime?model=...
//   - Receive session.created
//   - Send session.update (voice, format, sample_rate, mode=server_commit)
//   - Receive session.updated
//   - Send input_text_buffer.append (text)
//   - Send session.finish
//   - Receive response.audio.delta (base64 PCM) chunks
//   - Receive response.done, session.finished
//   - Close
package tts

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Client is a WebSocket client for the DashScope Qwen-TTS Realtime API.
type Client struct {
	wsURL      string
	model      string
	voice      string
	apiKey     string
	format     string
	sampleRate int
}

// SampleRate returns the output sample rate (e.g. 24000).
func (c *Client) SampleRate() int {
	return c.sampleRate
}

// NewClient creates a new DashScope Qwen-TTS realtime client.
// wsURL is the WebSocket URL.
// model is the model name (e.g. "qwen3-tts-flash-realtime").
// voice is the voice name (e.g. "Cherry").
// apiKey is the DashScope API key.
// format is the audio format ("pcm").
// sampleRate is the sample rate in Hz (24000).
func NewClient(wsURL, model, voice, apiKey string, format string, sampleRate int) *Client {
	return &Client{
		wsURL:      wsURL,
		model:      model,
		voice:      voice,
		apiKey:     apiKey,
		format:     format,
		sampleRate: sampleRate,
	}
}

// Close releases resources. WebSocket connections are ephemeral, so this is a no-op.
func (c *Client) Close() {}

// SynthesizeResult contains the result of a TTS synthesis.
type SynthesizeResult struct {
	// Samples are normalized float32 PCM samples in [-1, 1].
	Samples []float32
	// SampleRate is the sample rate in Hz (typically 24000).
	SampleRate int
	// Duration is the audio duration in seconds.
	Duration float64
}

// Synthesize converts text to speech via the Qwen-TTS Realtime WebSocket API.
// Returns PCM float32 samples compatible with the audio player.
func (c *Client) Synthesize(text string, speed float32) (*SynthesizeResult, error) {
	// Build URL with model query parameter.
	url := fmt.Sprintf("%s?model=%s", c.wsURL, c.model)

	header := make(http.Header)
	header.Set("Authorization", "Bearer "+c.apiKey)

	conn, resp, err := websocket.DefaultDialer.Dial(url, header)
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("tts: websocket dial HTTP %d: %w", resp.StatusCode, err)
		}
		return nil, fmt.Errorf("tts: websocket dial: %w", err)
	}
	defer conn.Close()
	log.Printf("tts: connected to %s", url)

	// Generate event IDs.
	eventID1 := fmt.Sprintf("event_%d", time.Now().UnixNano())

	// Collect all audio chunks.
	var allSamples []float32
	var mu sync.Mutex
	done := make(chan struct{})
	var readErr error

	// Read loop.
	go func() {
		defer close(done)
		for {
			msgType, msg, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
					return
				}
				readErr = fmt.Errorf("tts: read message: %w", err)
				return
			}
			if msgType != websocket.TextMessage {
				continue
			}

			var event map[string]interface{}
			if err := json.Unmarshal(msg, &event); err != nil {
				log.Printf("tts: parse event: %v", err)
				continue
			}

			eventType, _ := event["type"].(string)

			switch eventType {
			case "session.created":
				sess, _ := event["session"].(map[string]interface{})
				sessID, _ := sess["id"].(string)
				log.Printf("tts: session.created id=%s", sessID)

				// Send session.update.
				updateEvent := map[string]interface{}{
					"event_id": eventID1,
					"type":     "session.update",
					"session": map[string]interface{}{
						"voice":          c.voice,
						"mode":           "server_commit",
						"language_type":  "Chinese",
						"response_format": c.format,
						"sample_rate":    c.sampleRate,
					},
				}
				if err := conn.WriteJSON(updateEvent); err != nil {
					readErr = fmt.Errorf("tts: send session.update: %w", err)
					return
				}
				log.Printf("tts: sent session.update (voice=%s, mode=server_commit, format=%s, rate=%d)",
					c.voice, c.format, c.sampleRate)

			case "session.updated":
				log.Printf("tts: session.updated")

				// Send the text.
				appendEvent := map[string]interface{}{
					"event_id": fmt.Sprintf("event_%d", time.Now().UnixNano()),
					"type":     "input_text_buffer.append",
					"text":     text,
				}
				if err := conn.WriteJSON(appendEvent); err != nil {
					readErr = fmt.Errorf("tts: send input_text_buffer.append: %w", err)
					return
				}
				log.Printf("tts: sent input_text_buffer.append (%d chars)", len([]rune(text)))

				// Send session.finish.
				finishEvent := map[string]interface{}{
					"event_id": fmt.Sprintf("event_%d", time.Now().UnixNano()),
					"type":     "session.finish",
				}
				if err := conn.WriteJSON(finishEvent); err != nil {
					readErr = fmt.Errorf("tts: send session.finish: %w", err)
					return
				}
				log.Printf("tts: sent session.finish")

			case "response.audio.delta":
				deltaB64, _ := event["delta"].(string)
				if deltaB64 != "" {
					raw, err := base64.StdEncoding.DecodeString(deltaB64)
					if err != nil {
						log.Printf("tts: decode base64 audio: %v", err)
						continue
					}
					// Convert int16 PCM to float32.
					samples := pcmToFloat32(raw)
					mu.Lock()
					allSamples = append(allSamples, samples...)
					mu.Unlock()
				}

			case "response.audio.done":
				log.Printf("tts: response.audio.done")

			case "response.done":
				log.Printf("tts: response.done")

			case "session.finished":
				log.Printf("tts: session.finished")
				return

			case "error":
				errObj, _ := event["error"].(map[string]interface{})
				code, _ := errObj["code"].(string)
				msg, _ := errObj["message"].(string)
				readErr = fmt.Errorf("tts: server error [%s]: %s", code, msg)
				return

			default:
				log.Printf("tts: event type=%s", eventType)
			}
		}
	}()

	<-done

	if readErr != nil {
		return nil, readErr
	}

	dur := float64(len(allSamples)) / float64(c.sampleRate)
	log.Printf("tts: synthesized %d samples (%.1fs) for %d chars",
		len(allSamples), dur, len([]rune(text)))

	return &SynthesizeResult{
		Samples:    allSamples,
		SampleRate: c.sampleRate,
		Duration:   dur,
	}, nil
}

// pcmToFloat32 converts int16 little-endian PCM bytes to float32 samples in [-1, 1].
func pcmToFloat32(data []byte) []float32 {
	count := len(data) / 2
	samples := make([]float32, count)
	for i := range count {
		v := int16(binary.LittleEndian.Uint16(data[i*2:]))
		samples[i] = float32(v) / math.MaxInt16
	}
	return samples
}