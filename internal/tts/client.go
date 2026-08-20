// Package tts provides text-to-speech via the Alibaba Cloud Bailian (百炼)
// Qwen-TTS API (multimodal-generation endpoint).
package tts

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

// Client is an HTTP client for the Bailian Qwen-TTS API.
type Client struct {
	baseURL    string
	model      string
	voice      string
	apiKey     string
	httpClient *http.Client
	sampleRate int
}

// NewClient creates a new Bailian TTS client.
// model is the model name (e.g. "qwen3-tts-flash").
// voice is the voice name (e.g. "Cherry").
// apiKey is the DashScope API key.
func NewClient(baseURL, model, voice, apiKey string) *Client {
	return &Client{
		baseURL: baseURL,
		model:   model,
		voice:   voice,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
		sampleRate: 24000, // Qwen-TTS default is 24kHz
	}
}

// SampleRate returns the output sample rate (24kHz).
func (c *Client) SampleRate() int {
	return c.sampleRate
}

// Close releases resources held by the client.
func (c *Client) Close() {
	c.httpClient.CloseIdleConnections()
}

// SynthesizeResult contains the result of a TTS synthesis.
type SynthesizeResult struct {
	// Samples are normalized float32 PCM samples in [-1, 1].
	Samples []float32
	// SampleRate is the sample rate in Hz (typically 24000).
	SampleRate int
	// Duration is the audio duration in seconds.
	Duration float64
}

// Synthesize converts text to speech via the Bailian TTS API.
// Returns PCM float32 samples compatible with the audio player.
func (c *Client) Synthesize(text string, speed float32) (*SynthesizeResult, error) {
	// Build the DashScope multimodal-generation request.
	body := map[string]interface{}{
		"model": c.model,
		"input": map[string]interface{}{
			"text":          text,
			"voice":         c.voice,
			"language_type": "Chinese",
		},
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("tts: marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", c.baseURL, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("tts: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	log.Printf("tts: POST %s (model=%s, voice=%s, %d chars)",
		c.baseURL, c.model, c.voice, len([]rune(text)))
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tts: http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("tts: HTTP %d: %s", resp.StatusCode, string(errBody))
	}

	// Parse DashScope response.
	// Response shape: {"output": {"audio": {"url": "https://...wav", "data": ""}}}
	var result struct {
		Output struct {
			Audio struct {
				URL  string `json:"url"`
				Data string `json:"data"`
			} `json:"audio"`
		} `json:"output"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("tts: decode response: %w", err)
	}

	// Get the audio: either inline base64 data or a URL to download.
	var wavData []byte
	if result.Output.Audio.Data != "" {
		// Inline base64 audio (streaming mode middle chunks; unlikely in non-stream).
		wavData, err = base64Decode(result.Output.Audio.Data)
		if err != nil {
			return nil, fmt.Errorf("tts: decode base64 audio: %w", err)
		}
	} else if result.Output.Audio.URL != "" {
		// Download the WAV from the OSS URL.
		log.Printf("tts: downloading audio from %s", result.Output.Audio.URL)
		wavData, err = c.downloadAudio(result.Output.Audio.URL)
		if err != nil {
			return nil, err
		}
	} else {
		return nil, fmt.Errorf("tts: response contained no audio url or data")
	}

	// Parse WAV to PCM float32 samples.
	samples, sampleRate, err := wavToFloat32(wavData)
	if err != nil {
		return nil, err
	}
	if sampleRate > 0 {
		c.sampleRate = sampleRate
	}

	dur := float64(len(samples)) / float64(c.sampleRate)
	log.Printf("tts: synthesized %d samples (%.1fs) for %d chars",
		len(samples), dur, len([]rune(text)))

	return &SynthesizeResult{
		Samples:    samples,
		SampleRate: c.sampleRate,
		Duration:   dur,
	}, nil
}

// downloadAudio fetches the audio file from a URL.
func (c *Client) downloadAudio(url string) ([]byte, error) {
	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("tts: download audio: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tts: download audio HTTP %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

// wavToFloat32 parses a WAV file and returns float32 PCM samples in [-1, 1].
// It supports PCM 16-bit (and 8/32-bit) mono/stereo by reading the fmt chunk.
func wavToFloat32(wav []byte) ([]float32, int, error) {
	if len(wav) < 44 {
		return nil, 0, fmt.Errorf("tts: wav too short (%d bytes)", len(wav))
	}
	if string(wav[0:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
		return nil, 0, fmt.Errorf("tts: not a valid WAV file")
	}

	// Parse the fmt chunk to find format info.
	var channels uint16 = 1
	var bitsPerSample uint16 = 16
	var sampleRate uint32 = 24000
	var dataStart int = -1
	var dataSize int = 0

	pos := 12
	for pos+8 <= len(wav) {
		chunkID := string(wav[pos : pos+4])
		chunkSize := int(binary.LittleEndian.Uint32(wav[pos+4 : pos+8]))

		if chunkID == "fmt " && pos+24 <= len(wav) {
			channels = binary.LittleEndian.Uint16(wav[pos+10 : pos+12])
			sampleRate = binary.LittleEndian.Uint32(wav[pos+12 : pos+16])
			bitsPerSample = binary.LittleEndian.Uint16(wav[pos+22 : pos+24])
		}
		if chunkID == "data" {
			dataStart = pos + 8
			dataSize = chunkSize
			break
		}
		pos += 8 + chunkSize
		if chunkSize%2 == 1 {
			pos++ // padding byte
		}
	}

	if dataStart == -1 {
		return nil, 0, fmt.Errorf("tts: no data chunk found in WAV")
	}

	// Extract the raw audio bytes.
	end := dataStart + dataSize
	if end > len(wav) {
		end = len(wav)
	}
	raw := wav[dataStart:end]

	var samples []float32
	switch bitsPerSample {
	case 16:
		count := len(raw) / 2
		samples = make([]float32, 0, count)
		for i := 0; i+1 < len(raw); i += 2 {
			s := int16(binary.LittleEndian.Uint16(raw[i : i+2]))
			samples = append(samples, float32(s)/math.MaxInt16)
		}
	case 8:
		samples = make([]float32, 0, len(raw))
		for _, b := range raw {
			// 8-bit WAV is unsigned.
			samples = append(samples, float32(int8(b-128))/128.0)
		}
	case 32:
		count := len(raw) / 4
		samples = make([]float32, 0, count)
		for i := 0; i+3 < len(raw); i += 4 {
			s := int32(binary.LittleEndian.Uint32(raw[i : i+4]))
			samples = append(samples, float32(s)/math.MaxInt32)
		}
	default:
		return nil, 0, fmt.Errorf("tts: unsupported WAV bits per sample %d", bitsPerSample)
	}

	// If stereo, downsample to mono by averaging channels.
	if channels > 1 {
		mono := make([]float32, 0, len(samples)/int(channels))
		for i := 0; i+int(channels) <= len(samples); i += int(channels) {
			var sum float32
			for j := 0; j < int(channels); j++ {
				sum += samples[i+j]
			}
			mono = append(mono, sum/float32(channels))
		}
		samples = mono
	}

	return samples, int(sampleRate), nil
}

func base64Decode(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}