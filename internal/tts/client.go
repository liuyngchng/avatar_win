// Package tts provides text-to-speech via the Alibaba Cloud DashScope
// Qwen-TTS Realtime WebSocket API.
//
// Protocol: Qwen-TTS Realtime API (WebSocket), commit mode with persistent connection.
//   - Connect to wss://dashscope.aliyuncs.com/api-ws/v1/realtime?model=...
//   - Receive session.created
//   - Send session.update (voice, format, sample_rate, mode=commit)
//   - Receive session.updated
//   - For each Synthesize call:
//     - Send input_text_buffer.append (text)
//     - Send input_text_buffer.commit
//     - Receive response.audio.delta (base64 PCM) chunks
//     - Receive response.done
//   - On Close: send session.finish, receive session.finished, close
//
// The WebSocket connection is reused across Synthesize calls to avoid
// the ~200-500ms TLS+WS handshake on every request.
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
// The underlying WebSocket connection is kept alive across Synthesize calls.
type Client struct {
	wsURL      string
	model      string
	voice      string
	apiKey     string
	format     string
	sampleRate int

	mu   sync.Mutex
	conn *websocket.Conn
}

// SampleRate returns the output sample rate (e.g. 24000).
func (c *Client) SampleRate() int {
	return c.sampleRate
}

// NewClient creates a new DashScope Qwen-TTS realtime client.
// The connection is established lazily on the first Synthesize call.
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

// Close sends session.finish and closes the WebSocket connection.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closeLocked()
}

func (c *Client) closeLocked() {
	if c.conn == nil {
		return
	}
	// Best-effort: send session.finish.
	finishEvent := map[string]interface{}{
		"event_id": fmt.Sprintf("event_%d", time.Now().UnixNano()),
		"type":     "session.finish",
	}
	_ = c.conn.WriteJSON(finishEvent)
	// Give the server a moment to send session.finished.
	c.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		_, msg, err := c.conn.ReadMessage()
		if err != nil {
			break
		}
		var event map[string]interface{}
		if json.Unmarshal(msg, &event) == nil {
			if t, _ := event["type"].(string); t == "session.finished" {
				log.Printf("tts: session.finished (clean close)")
				break
			}
		}
	}
	c.conn.Close()
	c.conn = nil
}

// SynthesizeResult contains the result of a TTS synthesis.
type SynthesizeResult struct {
	Samples    []float32
	SampleRate int
	Duration   float64
}

// Synthesize converts text to speech via the Qwen-TTS Realtime WebSocket API.
// The WebSocket connection is reused across calls; only the first call pays
// the TLS+WS handshake cost.
func (c *Client) Synthesize(text string, speed float32) (*SynthesizeResult, error) {
	t0 := time.Now()

	c.mu.Lock()
	// Ensure we have a live connection (lazy connect / auto-reconnect).
	if err := c.ensureConnectedLocked(); err != nil {
		c.mu.Unlock()
		return nil, err
	}

	// Send the text.
	appendEvent := map[string]interface{}{
		"event_id": fmt.Sprintf("event_%d", time.Now().UnixNano()),
		"type":     "input_text_buffer.append",
		"text":     text,
	}
	if err := c.conn.WriteJSON(appendEvent); err != nil {
		log.Printf("tts: write append failed, reconnecting: %v", err)
		c.closeLocked()
		if err2 := c.ensureConnectedLocked(); err2 != nil {
			c.mu.Unlock()
			return nil, err2
		}
		if err := c.conn.WriteJSON(appendEvent); err != nil {
			c.mu.Unlock()
			return nil, fmt.Errorf("tts: send input_text_buffer.append: %w", err)
		}
	}
	log.Printf("tts: sent input_text_buffer.append (%d chars)", len([]rune(text)))

	// Commit to trigger synthesis.
	commitEvent := map[string]interface{}{
		"event_id": fmt.Sprintf("event_%d", time.Now().UnixNano()),
		"type":     "input_text_buffer.commit",
	}
	if err := c.conn.WriteJSON(commitEvent); err != nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("tts: send input_text_buffer.commit: %w", err)
	}
	log.Printf("tts: sent input_text_buffer.commit")

	// Snapshot the connection and release the lock before blocking on reads.
	// This prevents one hung synthesis from deadlocking the entire client.
	conn := c.conn
	c.mu.Unlock()

	// Read loop: collect audio deltas until response.done.
	// Set a 30s deadline so a hung server doesn't block forever.
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	var allSamples []float32
	readErr := c.readAudioLoopConn(conn, &allSamples)

	if readErr != nil {
		c.mu.Lock()
		c.closeLocked() // connection is in an unknown state, reconnect next time
		c.mu.Unlock()
		return nil, readErr
	}

	dur := float64(len(allSamples)) / float64(c.sampleRate)
	log.Printf("tts: synthesized %d samples (%.1fs) for %d chars",
		len(allSamples), dur, len([]rune(text)))
	log.Printf("⏱ [timing] TTS: total=%dms (commit + synth, no handshake)", time.Since(t0).Milliseconds())

	return &SynthesizeResult{
		Samples:    allSamples,
		SampleRate: c.sampleRate,
		Duration:   dur,
	}, nil
}

// ensureConnectedLocked connects to the TTS API and performs session setup.
// Must be called with c.mu held.
func (c *Client) ensureConnectedLocked() error {
	if c.conn != nil {
		return nil
	}

	t0 := time.Now()
	url := fmt.Sprintf("%s?model=%s", c.wsURL, c.model)
	header := make(http.Header)
	header.Set("Authorization", "Bearer "+c.apiKey)

	conn, resp, err := websocket.DefaultDialer.Dial(url, header)
	if err != nil {
		if resp != nil {
			return fmt.Errorf("tts: websocket dial HTTP %d: %w", resp.StatusCode, err)
		}
		return fmt.Errorf("tts: websocket dial: %w", err)
	}
	log.Printf("tts: connected to %s (%dms)", url, time.Since(t0).Milliseconds())

	// Wait for session.created.
	_, msg, err := conn.ReadMessage()
	if err != nil {
		conn.Close()
		return fmt.Errorf("tts: wait session.created: %w", err)
	}
	var event map[string]interface{}
	if err := json.Unmarshal(msg, &event); err != nil {
		conn.Close()
		return fmt.Errorf("tts: parse session.created: %w", err)
	}
	et, _ := event["type"].(string)
	if et != "session.created" {
		conn.Close()
		return fmt.Errorf("tts: expected session.created, got %q", et)
	}
	sess, _ := event["session"].(map[string]interface{})
	sid, _ := sess["id"].(string)
	log.Printf("tts: session.created id=%s", sid)

	// Send session.update (commit mode).
	updateEvent := map[string]interface{}{
		"event_id": fmt.Sprintf("event_%d", time.Now().UnixNano()),
		"type":     "session.update",
		"session": map[string]interface{}{
			"voice":           c.voice,
			"mode":            "commit",
			"language_type":   "Chinese",
			"response_format": c.format,
			"sample_rate":     c.sampleRate,
		},
	}
	if err := conn.WriteJSON(updateEvent); err != nil {
		conn.Close()
		return fmt.Errorf("tts: send session.update: %w", err)
	}
	log.Printf("tts: sent session.update (voice=%s, mode=commit, format=%s, rate=%d)",
		c.voice, c.format, c.sampleRate)

	// Wait for session.updated.
	_, msg, err = conn.ReadMessage()
	if err != nil {
		conn.Close()
		return fmt.Errorf("tts: wait session.updated: %w", err)
	}
	if err := json.Unmarshal(msg, &event); err != nil {
		conn.Close()
		return fmt.Errorf("tts: parse session.updated: %w", err)
	}
	et, _ = event["type"].(string)
	if et == "error" {
		errObj, _ := event["error"].(map[string]interface{})
		code, _ := errObj["code"].(string)
		msg, _ := errObj["message"].(string)
		conn.Close()
		return fmt.Errorf("tts: session.update error [%s]: %s", code, msg)
	}
	if et != "session.updated" {
		conn.Close()
		return fmt.Errorf("tts: expected session.updated, got %q", et)
	}
	log.Printf("tts: session.updated")

	c.conn = conn
	log.Printf("⏱ [timing] TTS: ws_connect+handshake=%dms", time.Since(t0).Milliseconds())
	return nil
}

// readAudioLoopConn reads messages from the given connection until response.done,
// collecting audio deltas along the way. Returns an error if the server
// reports one or the connection is broken. The caller must set a read deadline
// on conn before calling to avoid hanging forever.
func (c *Client) readAudioLoopConn(conn *websocket.Conn, allSamples *[]float32) error {
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("tts: read message: %w", err)
		}

		var event map[string]interface{}
		if err := json.Unmarshal(msg, &event); err != nil {
			log.Printf("tts: parse event: %v", err)
			continue
		}

		eventType, _ := event["type"].(string)

		switch eventType {
		case "response.audio.delta":
			deltaB64, _ := event["delta"].(string)
			if deltaB64 == "" {
				continue
			}
			raw, err := base64.StdEncoding.DecodeString(deltaB64)
			if err != nil {
				log.Printf("tts: decode base64 audio: %v", err)
				continue
			}
			samples := pcmToFloat32(raw)
			*allSamples = append(*allSamples, samples...)

		case "response.audio.done":
			log.Printf("tts: response.audio.done")

		case "response.done":
			log.Printf("tts: response.done")
			return nil

		case "error":
			errObj, _ := event["error"].(map[string]interface{})
			code, _ := errObj["code"].(string)
			msg, _ := errObj["message"].(string)
			return fmt.Errorf("tts: server error [%s]: %s", code, msg)

		default:
			// response.created, response.output_item.added,
			// response.content_part.added, response.content_part.done,
			// response.output_item.done — all informational, skip.
		}
	}
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