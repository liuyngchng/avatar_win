// Package llm provides a chat client for an OpenAI-compatible HTTP API.
package llm

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// Client is an HTTP client for an OpenAI-compatible chat completions API.
type Client struct {
	baseURL    string // full URL to the chat/completions endpoint
	model      string
	apiKey     string
	system     string
	httpClient *http.Client
}

// NewClient creates a new online LLM client.
func NewClient(baseURL, model, apiKey, name string) *Client {
	if name == "" {
		name = "小然"
	}
	return &Client{
		baseURL: baseURL,
		model:   model,
		apiKey:  apiKey,
		system: "你是一个语音助手，名字叫「" + name + "」。用口语化的中文回复，自然友好、直接明了。" +
			"闲聊或简单问题控制在1-3句话（80字以内）；" +
			"知识类问题可以适当展开解释，但保持简洁，不超过150字。" +
			"围绕用户的问题回答，不要偏离话题。" +
			"这是一个多轮对话，记住之前聊过的话题，保持一致的语气。",
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// Close releases resources held by the client.
func (c *Client) Close() {
	c.httpClient.CloseIdleConnections()
}

// chatMessage is a single message in the conversation.
type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatRequest is the JSON body sent to the chat completions endpoint.
type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Stream      bool          `json:"stream"`
	MaxTokens   int           `json:"max_tokens"`
	Temperature float64       `json:"temperature"`
	TopP        float64       `json:"top_p"`
}

// chatResponse is the JSON response from a non-streaming chat request.
type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// streamChunk is a single SSE delta from the streaming API.
type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
}

// Chat sends a single user message and returns the assistant's reply.
func (c *Client) Chat(userText string) (string, error) {
	messages := []chatMessage{
		{Role: "system", Content: c.system},
		{Role: "user", Content: userText},
	}
	return c.chat(messages)
}

// chat performs a non-streaming completion request.
func (c *Client) chat(messages []chatMessage) (string, error) {
	t0 := time.Now()

	body := chatRequest{
		Model:       c.model,
		Messages:    messages,
		Stream:      false,
		MaxTokens:   512,
		Temperature: 0.7,
		TopP:        0.9,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("llm: marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", c.baseURL, bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("llm: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	log.Printf("llm: POST %s (model=%s)", c.baseURL, c.model)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("llm: http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("llm: HTTP %d: %s", resp.StatusCode, string(errBody))
	}

	var result chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("llm: decode response: %w", err)
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("llm: empty response (no choices)")
	}

	reply := result.Choices[0].Message.Content
	log.Printf("llm: reply %d chars", len([]rune(reply)))
	log.Printf("⏱ [timing] LLM: total=%dms (api_call + decode)", time.Since(t0).Milliseconds())
	return reply, nil
}

// ChatStream sends a user message and returns a channel that receives text
// chunks as they arrive from the streaming API. The channel is closed when
// the stream ends. The caller should drain the channel until it's closed.
func (c *Client) ChatStream(userText string) <-chan string {
	ch := make(chan string, 16)

	messages := []chatMessage{
		{Role: "system", Content: c.system},
		{Role: "user", Content: userText},
	}

	go func() {
		defer close(ch)
		t0 := time.Now()

		body := chatRequest{
			Model:       c.model,
			Messages:    messages,
			Stream:      true,
			MaxTokens:   512,
			Temperature: 0.7,
			TopP:        0.9,
		}

		jsonBody, err := json.Marshal(body)
		if err != nil {
			log.Printf("llm: marshal stream request: %v", err)
			return
		}

		req, err := http.NewRequest("POST", c.baseURL, bytes.NewReader(jsonBody))
		if err != nil {
			log.Printf("llm: create stream request: %v", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		if c.apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.apiKey)
		}

		log.Printf("llm: POST %s (model=%s, stream=true)", c.baseURL, c.model)
		resp, err := c.httpClient.Do(req)
		if err != nil {
			log.Printf("llm: stream http request: %v", err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
			log.Printf("llm: stream HTTP %d: %s", resp.StatusCode, string(errBody))
			return
		}

		firstToken := true
		totalChars := 0
		scanner := bufio.NewScanner(resp.Body)
		// SSE lines can be very long; use a larger buffer.
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		for scanner.Scan() {
			line := scanner.Text()
			if line == "" || strings.HasPrefix(line, ":") {
				continue
			}
			const prefix = "data: "
			if !strings.HasPrefix(line, prefix) {
				continue
			}
			data := strings.TrimSpace(line[len(prefix):])
			if data == "[DONE]" {
				break
			}

			var chunk streamChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				log.Printf("llm: parse stream chunk: %v", err)
				continue
			}

			if len(chunk.Choices) > 0 {
				text := chunk.Choices[0].Delta.Content
				if text != "" {
					if firstToken {
						log.Printf("⏱ [timing] LLM: first_token=%dms", time.Since(t0).Milliseconds())
						firstToken = false
					}
					totalChars += len([]rune(text))
					ch <- text
				}
			}
		}

		if err := scanner.Err(); err != nil {
			log.Printf("llm: stream scanner: %v", err)
		}

		log.Printf("llm: stream reply %d chars, total=%dms", totalChars, time.Since(t0).Milliseconds())
	}()

	return ch
}