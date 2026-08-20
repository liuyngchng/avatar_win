// Package asr provides automatic speech recognition via the Alibaba Cloud
// Bailian (百炼) Qwen-ASR API, using the OpenAI-compatible chat/completions
// endpoint with the input_audio content type.
package asr

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"time"
)

// Client is an HTTP client for the Bailian Qwen-ASR API.
type Client struct {
	baseURL    string
	model      string
	apiKey     string
	httpClient *http.Client
}

// NewClient creates a new Bailian ASR client.
// model is the model name (e.g. "qwen3-asr-flash").
// apiKey is the DashScope API key.
func NewClient(baseURL, model, apiKey string) *Client {
	return &Client{
		baseURL: baseURL,
		model:   model,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Close releases resources held by the client.
func (c *Client) Close() {
	c.httpClient.CloseIdleConnections()
}

// Transcribe sends PCM float32 audio samples to the ASR API and returns
// the transcribed text. samples are normalized in [-1, 1], sampleRate
// is the audio sample rate in Hz.
func (c *Client) Transcribe(samples []float32, sampleRate int) (string, error) {
	// Convert float32 samples to int16 WAV, then base64 encode.
	wavData := float32ToWAV(samples, sampleRate)
	dataURI := "data:audio/wav;base64," + base64.StdEncoding.EncodeToString(wavData)

	body := map[string]interface{}{
		"model": c.model,
		"messages": []map[string]interface{}{
			{
				"role": "user",
				"content": []map[string]interface{}{
					{
						"type":         "input_audio",
						"input_audio": map[string]string{"data": dataURI},
					},
				},
			},
		},
		"stream": false,
		"asr_options": map[string]interface{}{
			"language":   "zh",
			"enable_itn": true,
		},
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("asr: marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", c.baseURL, bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("asr: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	log.Printf("asr: POST %s (model=%s, %d samples, %d Hz)",
		c.baseURL, c.model, len(samples), sampleRate)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("asr: http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("asr: HTTP %d: %s", resp.StatusCode, string(errBody))
	}

	// Parse OpenAI-compatible chat completion response.
	// Response shape: {"choices": [{"message": {"content": "识别文本"}}]}
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("asr: decode response: %w", err)
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("asr: empty response (no choices)")
	}

	text := result.Choices[0].Message.Content
	log.Printf("asr: transcribed: %q", text)
	return text, nil
}

// float32ToWAV converts float32 PCM samples to a WAV file (16-bit, mono, PCM).
func float32ToWAV(samples []float32, sampleRate int) []byte {
	var buf bytes.Buffer

	numSamples := len(samples)
	dataSize := numSamples * 2 // 16-bit = 2 bytes per sample
	fileSize := 44 + dataSize

	// RIFF header
	buf.WriteString("RIFF")
	binary.Write(&buf, binary.LittleEndian, uint32(fileSize-8))
	buf.WriteString("WAVE")

	// fmt chunk
	buf.WriteString("fmt ")
	binary.Write(&buf, binary.LittleEndian, uint32(16))
	binary.Write(&buf, binary.LittleEndian, uint16(1))          // PCM
	binary.Write(&buf, binary.LittleEndian, uint16(1))          // mono
	binary.Write(&buf, binary.LittleEndian, uint32(sampleRate))
	binary.Write(&buf, binary.LittleEndian, uint32(sampleRate*2))
	binary.Write(&buf, binary.LittleEndian, uint16(2))
	binary.Write(&buf, binary.LittleEndian, uint16(16))

	// data chunk
	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, uint32(dataSize))

	for _, s := range samples {
		if s > 1.0 {
			s = 1.0
		} else if s < -1.0 {
			s = -1.0
		}
		v := int16(s * math.MaxInt16)
		binary.Write(&buf, binary.LittleEndian, v)
	}

	return buf.Bytes()
}