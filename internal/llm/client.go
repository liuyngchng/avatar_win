// Package llm provides an OpenAI-compatible chat completions client with
// SSE streaming support.
package llm

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Client is an HTTP client for an OpenAI-compatible chat completions API.
type Client struct {
	baseURL    string // full URL to the chat/completions endpoint
	model      string
	apiKey     string
	system     string
	maxTokens  int
	httpClient *http.Client

	mu      sync.Mutex    // guards history
	history []chatMessage // recent conversation turns, oldest first
}

// maxHistoryTurns is how many recent turns (a "turn" = one user message +
// one assistant reply) are kept as context for the next request.
const maxHistoryTurns = 10

// defaultMaxTokens is used when no max_tokens is configured in cfg.yml.
const defaultMaxTokens = 512

// NewClient creates a new online LLM client.
// proxyFunc is the http.Proxy function used for HTTP requests
// (e.g. config.ProxyFunc(cfg.Proxy)).
func NewClient(baseURL, model, apiKey, name string, maxTokens int, proxyFunc func(*http.Request) (*url.URL, error)) *Client {
	if name == "" {
		name = "小然"
	}
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}
	now := time.Now()
	systemPrompt := fmt.Sprintf(
		"今天是%s %s。你是一个语音助手，名字叫「%s」。用口语化的中文回复，自然友好、直接明了。"+
			"闲聊或简单问题控制在1-3句话（80字以内）；"+
			"知识类问题可以适当展开解释，但保持简洁，不超过150字。"+
			"围绕用户的问题回答，不要偏离话题。"+
			"这是一个多轮对话，记住之前聊过的话题，保持一致的语气。"+
			"回复时可以在开头用[emotion:表情]标签标注情绪，可选表情：neutral/happy/angry/sad/surprised/relaxed。"+
			"例如：[emotion:happy]你好呀！今天天气真不错！",
		now.Format("2006年1月2日"), weekdayCN(now.Weekday()), name)
	return &Client{
		baseURL:   baseURL,
		model:     model,
		apiKey:    apiKey,
		maxTokens: maxTokens,
		system:    systemPrompt,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
			Transport: &http.Transport{
				Proxy: proxyFunc,
				// The intranet API uses a self-signed TLS certificate;
				// skip verification (same as `curl -k`).
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
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
// It uses and updates the conversation history.
func (c *Client) Chat(userText string) (string, error) {
	messages := c.buildMessages(userText)
	reply, err := c.chat(messages)
	if err != nil {
		return "", err
	}
	c.recordTurn(userText, reply)
	return reply, nil
}

// buildMessages assembles the full message list for a request: the system
// prompt, the recent conversation history, and the new user message.
func (c *Client) buildMessages(userText string) []chatMessage {
	c.mu.Lock()
	defer c.mu.Unlock()

	messages := make([]chatMessage, 0, len(c.history)+2)
	messages = append(messages, chatMessage{Role: "system", Content: c.system})
	messages = append(messages, c.history...)
	messages = append(messages, chatMessage{Role: "user", Content: userText})
	return messages
}

// recordTurn appends a completed user→assistant turn to the history,
// keeping at most maxHistoryTurns turns (oldest dropped first).
func (c *Client) recordTurn(userText, replyText string) {
	if replyText == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	c.history = append(c.history,
		chatMessage{Role: "user", Content: userText},
		chatMessage{Role: "assistant", Content: replyText},
	)

	// Each turn adds 2 messages; trim to maxHistoryTurns turns.
	maxMessages := maxHistoryTurns * 2
	if len(c.history) > maxMessages {
		c.history = c.history[len(c.history)-maxMessages:]
	}
}

// RecordTurn appends a completed user→assistant turn to the history. It is
// used by the streaming path, where the reply is assembled by the caller
// rather than returned by the client.
func (c *Client) RecordTurn(userText, replyText string) {
	c.recordTurn(userText, replyText)
}

// ResetHistory clears the conversation history.
func (c *Client) ResetHistory() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.history = c.history[:0]
}

// chat performs a non-streaming completion request.
func (c *Client) chat(messages []chatMessage) (string, error) {
	t0 := time.Now()

	body := chatRequest{
		Model:       c.model,
		Messages:    messages,
		Stream:      false,
		MaxTokens:   c.maxTokens,
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

	slog.Debug("client_chat_llm:_POST", "url", c.baseURL, "model", c.model)
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
	slog.Debug("client_chat_llm:_reply", "chars", len([]rune(reply)))
	slog.Debug("client_chat_⏱_[timing]_LLM:_total", "ms", time.Since(t0).Milliseconds())
	return reply, nil
}

// ChatStream sends a user message and returns a channel that receives text
// chunks as they arrive from the streaming API, and an error channel that
// receives any error that occurs during the stream. The chunk channel is
// closed when the stream ends. The caller should drain the chunk channel
// until it's closed, then check the error channel.
//
// The caller MUST call RecordTurn(userText, replyText) after consuming the
// stream to store the conversation history for future requests.
func (c *Client) ChatStream(userText string) (<-chan string, <-chan error) {
	ch := make(chan string, 16)
	errCh := make(chan error, 1)

	messages := c.buildMessages(userText)

	go func() {
		defer close(ch)
		defer close(errCh)
		t0 := time.Now()

		body := chatRequest{
			Model:       c.model,
			Messages:    messages,
			Stream:      true,
			MaxTokens:   c.maxTokens,
			Temperature: 0.7,
			TopP:        0.9,
		}

		jsonBody, err := json.Marshal(body)
		if err != nil {
			errCh <- fmt.Errorf("llm: marshal stream request: %w", err)
			return
		}

		req, err := http.NewRequest("POST", c.baseURL, bytes.NewReader(jsonBody))
		if err != nil {
			errCh <- fmt.Errorf("llm: create stream request: %w", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		if c.apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.apiKey)
		}

		slog.Debug("client_ChatStream_llm:_POST", "url", c.baseURL, "model", c.model, "stream", true)
		resp, err := c.httpClient.Do(req)
		if err != nil {
			errCh <- fmt.Errorf("llm: stream http request: %w", err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
			errCh <- fmt.Errorf("llm: HTTP %d: %s", resp.StatusCode, string(errBody))
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
				slog.Error("client_ChatStream_llm:_parse_stream_chunk", "error", err)
				continue
			}

			if len(chunk.Choices) > 0 {
				// Skip reasoning tokens (e.g. DeepSeek thinking),
				// only collect actual content.
				text := chunk.Choices[0].Delta.Content
				if text != "" {
					if firstToken {
						slog.Debug("client_ChatStream_⏱_[timing]_LLM:_first_token", "ms", time.Since(t0).Milliseconds())
						firstToken = false
					}
					totalChars += len([]rune(text))
					ch <- text
				}
			}
		}

		if err := scanner.Err(); err != nil {
			errCh <- fmt.Errorf("llm: SSE read error: %w", err)
		}

		slog.Debug("client_ChatStream_llm:_stream_reply", "chars", totalChars, "total_ms", time.Since(t0).Milliseconds())
	}()

	return ch, errCh
}

// weekdayCN returns the Chinese name for a time.Weekday.
func weekdayCN(d time.Weekday) string {
	names := [...]string{"星期日", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六"}
	return names[d]
}

// ParseEmotion extracts the emotion tag from the beginning of the response text.
// Returns the emotion and the cleaned text. If no tag is found, returns
// "neutral" and the original text.
func ParseEmotion(text string) (emotion string, cleanText string) {
	text = strings.TrimSpace(text)
	const prefix = "[emotion:"
	if strings.HasPrefix(text, prefix) {
		end := strings.Index(text, "]")
		if end > 0 {
			raw := text[len(prefix):end]
			cleanText = strings.TrimSpace(text[end+1:])
			switch raw {
			case "neutral", "happy", "angry", "sad", "surprised", "relaxed":
				return raw, cleanText
			default:
				// Unknown tag: still strip it, keep emotion neutral.
				return "neutral", cleanText
			}
		}
	}
	return "neutral", text
}
