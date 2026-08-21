// Package asr provides automatic speech recognition via the Alibaba Cloud
// DashScope Qwen-Audio-3.0-ASR-Flash-Streaming WebSocket API.
//
// Protocol: WebSocket "run-task" duplex streaming.
//   - Connect to wss://.../api-ws/v1/inference
//   - Send run-task JSON (model, format=pcm, sample_rate=16000)
//   - Wait for task-started
//   - Send binary audio chunks (PCM 16-bit, 16kHz, mono)
//   - Receive result-generated events with sentence.text
//   - Send finish-task when done
//   - Wait for task-finished and close
package asr

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

// Client is a WebSocket client for the DashScope realtime ASR API.
type Client struct {
	wsURL      string
	model      string
	apiKey     string
	format     string
	sampleRate int
}

// NewClient creates a new DashScope realtime ASR client.
// wsURL is the WebSocket URL (e.g. "wss://...maas.aliyuncs.com/api-ws/v1/inference").
// model is the model name (e.g. "qwen-audio-3.0-asr-flash-streaming").
// apiKey is the DashScope API key.
// format is the audio format ("pcm").
// sampleRate is the audio sample rate in Hz (16000).
func NewClient(wsURL, model, apiKey string, format string, sampleRate int) *Client {
	return &Client{
		wsURL:      wsURL,
		model:      model,
		apiKey:     apiKey,
		format:     format,
		sampleRate: sampleRate,
	}
}

// Close releases resources. WebSocket connections are ephemeral, so this is a no-op.
func (c *Client) Close() {}

// Transcribe sends PCM float32 audio samples to the ASR API via WebSocket
// and returns the transcribed text. samples are normalized in [-1, 1],
// sampleRate is the audio sample rate in Hz.
func (c *Client) Transcribe(samples []float32, sampleRate int) (string, error) {
	// Connect WebSocket.
	header := make(http.Header)
	header.Set("Authorization", "Bearer "+c.apiKey)

	conn, resp, err := websocket.DefaultDialer.Dial(c.wsURL, header)
	if err != nil {
		if resp != nil {
			return "", fmt.Errorf("asr: websocket dial HTTP %d: %w", resp.StatusCode, err)
		}
		return "", fmt.Errorf("asr: websocket dial: %w", err)
	}
	defer conn.Close()

	// Generate task ID.
	taskID := generateID()

	// Send run-task.
	runTask := map[string]interface{}{
		"header": map[string]interface{}{
			"action":    "run-task",
			"task_id":   taskID,
			"streaming": "duplex",
		},
		"payload": map[string]interface{}{
			"task_group": "audio",
			"task":       "asr",
			"function":   "recognition",
			"model":      c.model,
			"parameters": map[string]interface{}{
				"sample_rate": c.sampleRate,
				"format":      c.format,
			},
			"input": map[string]interface{}{},
		},
	}
	if err := conn.WriteJSON(runTask); err != nil {
		return "", fmt.Errorf("asr: send run-task: %w", err)
	}
	log.Printf("asr: sent run-task (task=%s, model=%s)", taskID, c.model)

	// Read messages: wait for task-started, then send audio, then collect results.
	var finalText string
	taskStarted := false
	audioSent := false
	done := make(chan struct{})
	var readErr error

	// Read messages in a goroutine.
	go func() {
		defer close(done)
		for {
			msgType, msg, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
					return
				}
				readErr = fmt.Errorf("asr: read message: %w", err)
				return
			}

			// Binary messages are not expected in this direction.
			if msgType != websocket.TextMessage {
				continue
			}

			var event map[string]interface{}
			if err := json.Unmarshal(msg, &event); err != nil {
				log.Printf("asr: parse event: %v", err)
				continue
			}

			header, _ := event["header"].(map[string]interface{})
			eventName, _ := header["event"].(string)

			switch eventName {
			case "task-started":
				log.Printf("asr: task-started")
				taskStarted = true
				// Send audio in a separate goroutine so we can keep reading.
				if !audioSent {
					audioSent = true
					go func() {
						if err := c.sendAudio(conn, samples, c.sampleRate); err != nil {
							log.Printf("asr: send audio: %v", err)
						}
						// Send finish-task.
						finishTask := map[string]interface{}{
							"header": map[string]interface{}{
								"action":    "finish-task",
								"task_id":   taskID,
								"streaming": "duplex",
							},
							"payload": map[string]interface{}{
								"input": map[string]interface{}{},
							},
						}
						if err := conn.WriteJSON(finishTask); err != nil {
							log.Printf("asr: send finish-task: %v", err)
						}
						log.Printf("asr: sent finish-task")
					}()
				}

			case "result-generated":
				payload, _ := event["payload"].(map[string]interface{})
				output, _ := payload["output"].(map[string]interface{})
				sentence, _ := output["sentence"].(map[string]interface{})
				text, _ := sentence["text"].(string)
				sentenceEnd, _ := sentence["sentence_end"].(bool)
				log.Printf("asr: result-generated text=%q sentence_end=%v", text, sentenceEnd)
				if sentenceEnd {
					// Accumulate final sentences.
					if finalText != "" {
						finalText += " "
					}
					finalText += text
				}

			case "task-finished":
				log.Printf("asr: task-finished")
				return

			case "task-failed":
				errMsg, _ := header["error_message"].(string)
				readErr = fmt.Errorf("asr: task failed: %s", errMsg)
				return

			default:
				log.Printf("asr: unknown event: %s", eventName)
			}
		}
	}()

	// Wait for the read goroutine to finish.
	<-done

	if readErr != nil {
		return "", readErr
	}

	if !taskStarted {
		return "", fmt.Errorf("asr: task never started")
	}

	log.Printf("asr: final text: %q", finalText)
	return finalText, nil
}

// sendAudio converts float32 samples to int16 PCM and sends them as binary
// WebSocket frames in chunks of ~100ms.
func (c *Client) sendAudio(conn *websocket.Conn, samples []float32, sampleRate int) error {
	// Convert float32 to int16 PCM bytes.
	pcm := float32ToPCM(samples)

	// Chunk size: 100ms = sampleRate * 2 bytes per sample / 10
	chunkSize := sampleRate * 2 / 10 // 3200 for 16kHz
	if chunkSize < 1 {
		chunkSize = 3200
	}

	for offset := 0; offset < len(pcm); offset += chunkSize {
		end := offset + chunkSize
		if end > len(pcm) {
			end = len(pcm)
		}
		chunk := pcm[offset:end]
		if err := conn.WriteMessage(websocket.BinaryMessage, chunk); err != nil {
			return fmt.Errorf("send binary audio: %w", err)
		}
		// Simulate real-time pacing: 100ms per chunk.
		time.Sleep(100 * time.Millisecond)
	}

	log.Printf("asr: sent %d bytes of PCM audio in %d-byte chunks", len(pcm), chunkSize)
	return nil
}

// float32ToPCM converts float32 samples in [-1, 1] to int16 little-endian PCM bytes.
func float32ToPCM(samples []float32) []byte {
	buf := make([]byte, len(samples)*2)
	for i, s := range samples {
		if s > 1.0 {
			s = 1.0
		} else if s < -1.0 {
			s = -1.0
		}
		v := int16(s * math.MaxInt16)
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(v))
	}
	return buf
}

// generateID creates a short random hex ID for task identification.
func generateID() string {
	// Use nanosecond timestamp as a simple unique ID.
	return fmt.Sprintf("%016x", time.Now().UnixNano())
}