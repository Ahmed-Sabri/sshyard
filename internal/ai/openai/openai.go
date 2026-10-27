// Package openai is an OpenAI-compatible chat client: one streaming
// completion against the configured base URL, with function tools.
// The API key is a header only. It is never written to a log, and errors
// that would quote it are scrubbed.
package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// DefaultMaxBytes is the most of one response body a turn will read.
const DefaultMaxBytes = 8 << 20

// errorBodyCap is how much of a non-2xx body is kept for the message.
const errorBodyCap = 64 << 10

// ErrTooBig is the error when a response exceeds the size cap.
var ErrTooBig = errors.New("openai: response exceeds the size cap")

// ErrBaseURL is the error for a base URL this client will not dial.
var ErrBaseURL = errors.New("openai: base URL must be http or https with a host")

// Client talks to one OpenAI-compatible endpoint. A zero Temperature or
// MaxTokens is omitted so the provider's default applies.
type Client struct {
	BaseURL     string
	APIKey      string
	Model       string
	Headers     map[string]string
	Temperature float64
	MaxTokens   int
	// MaxBytes caps the response body of one call. Zero means DefaultMaxBytes.
	MaxBytes int
	// HTTP is optional. Its Transport is reused; a redirect is allowed
	// only when its scheme, hostname and port all match BaseURL.
	HTTP *http.Client
}

// Message is one chat message. ToolCalls is set on an assistant message
// that asked for tools; ToolCallID is set on a tool result.
type Message struct {
	Role       string
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
	Finish     string
}

// ToolCall is one function call, arguments accumulated from the stream.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// Tool is a function the model may call. Parameters is a JSON schema.
type Tool struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

// Request is one completion.
type Request struct {
	Messages []Message
	Tools    []Tool
	// MaxTokens overrides the client's when not zero.
	MaxTokens int
}

// Event is one piece of streamed text. Thinking is set when the model
// sends a reasoning delta.
type Event struct {
	Content  string
	Thinking string
}

// APIError is a non-2xx response. Error never contains the API key.
type APIError struct {
	Status    int
	Message   string
	Truncated bool
	key       string
}

func (e *APIError) Error() string {
	msg := e.Message
	if e.key != "" {
		msg = strings.ReplaceAll(msg, e.key, "[redacted]")
	}
	if e.Truncated {
		return fmt.Sprintf("openai: %d: %s (truncated)", e.Status, msg)
	}
	return fmt.Sprintf("openai: %d: %s", e.Status, msg)
}

// Test checks the endpoint with a tiny request and no tools.
func (c *Client) Test(ctx context.Context) error {
	_, err := c.Stream(ctx, Request{Messages: []Message{{Role: "user", Content: "ping"}}, MaxTokens: 1}, nil)
	return err
}

// Stream posts {base}/chat/completions with stream:true and returns the
// assembled assistant message. emit, when set, receives each text delta.
// Cancelling ctx stops the read. A redirect is refused unless its scheme,
// hostname and port all match BaseURL, so the key is not sent in clear or
// to another service.
func (c *Client) Stream(ctx context.Context, req Request, emit func(Event)) (Message, error) {
	if c.Model == "" {
		return Message{}, errors.New("openai: model is empty")
	}
	endpoint, err := c.endpoint()
	if err != nil {
		return Message{}, err
	}
	payload, err := c.payload(req)
	if err != nil {
		return Message{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		return Message{}, c.scrub(err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	for k, v := range c.Headers {
		if strings.EqualFold(k, "Authorization") || strings.ContainsAny(k+v, "\r\n\x00") {
			continue
		}
		httpReq.Header.Set(k, v)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.http(endpoint).Do(httpReq)
	if err != nil {
		return Message{}, c.scrub(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return Message{}, c.httpError(resp)
	}
	msg, err := c.readStream(resp.Body, emit)
	if err != nil {
		return Message{}, c.scrub(err)
	}
	return msg, nil
}

func (c *Client) endpoint() (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(c.BaseURL))
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return nil, ErrBaseURL
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/chat/completions"
	u.RawQuery, u.Fragment = "", ""
	return u, nil
}

func (c *Client) http(base *url.URL) *http.Client {
	var transport http.RoundTripper
	if c.HTTP != nil {
		transport = c.HTTP.Transport
	}
	return &http.Client{Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("openai: too many redirects")
		}
		if req.URL.Scheme != base.Scheme || !strings.EqualFold(req.URL.Hostname(), base.Hostname()) || urlPort(req.URL) != urlPort(base) {
			return fmt.Errorf("openai: refusing redirect to %s", req.URL.Scheme+"://"+req.URL.Host)
		}
		return nil
	}}
}

// urlPort is the port a redirect is judged by. An empty port is the
// scheme default, so https://host and https://host:443 are the same service.
func urlPort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if u.Scheme == "http" {
		return "80"
	}
	return "443"
}

func (c *Client) maxBytes() int {
	if c.MaxBytes > 0 {
		return c.MaxBytes
	}
	return DefaultMaxBytes
}

func (c *Client) payload(req Request) ([]byte, error) {
	maxTok := c.MaxTokens
	if req.MaxTokens > 0 {
		maxTok = req.MaxTokens
	}
	body := map[string]any{
		"model":    c.Model,
		"messages": wireMessages(req.Messages),
		"stream":   true,
	}
	if c.Temperature > 0 {
		body["temperature"] = c.Temperature
	}
	if maxTok > 0 {
		body["max_tokens"] = maxTok
	}
	if len(req.Tools) > 0 {
		body["tools"] = wireTools(req.Tools)
	}
	return json.Marshal(body)
}

type wireMsg struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []wireCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type wireCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func wireMessages(in []Message) []wireMsg {
	out := make([]wireMsg, len(in))
	for i, m := range in {
		out[i] = wireMsg{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID}
		for _, tc := range m.ToolCalls {
			w := wireCall{ID: tc.ID, Type: "function"}
			w.Function.Name = tc.Name
			w.Function.Arguments = tc.Arguments
			out[i].ToolCalls = append(out[i].ToolCalls, w)
		}
	}
	return out
}

func wireTools(in []Tool) []map[string]any {
	out := make([]map[string]any, len(in))
	for i, t := range in {
		fn := map[string]any{"name": t.Name}
		if t.Description != "" {
			fn["description"] = t.Description
		}
		if len(t.Parameters) > 0 {
			fn["parameters"] = json.RawMessage(t.Parameters)
		}
		out[i] = map[string]any{"type": "function", "function": fn}
	}
	return out
}

func (c *Client) httpError(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, errorBodyCap+1))
	truncated := len(b) > errorBodyCap
	if truncated {
		b = b[:errorBodyCap]
	}
	msg := strings.TrimSpace(string(b))
	var eb struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(b, &eb) == nil {
		switch {
		case eb.Error.Message != "":
			msg = eb.Error.Message
		case eb.Message != "":
			msg = eb.Message
		}
	}
	return &APIError{Status: resp.StatusCode, Message: msg, Truncated: truncated, key: c.APIKey}
}

type sseChunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			Reasoning string `json:"reasoning_content"`
			Reason    string `json:"reasoning"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type callAcc struct {
	id, name string
	args     strings.Builder
	seen     bool
}

func (c *Client) readStream(body io.Reader, emit func(Event)) (Message, error) {
	r := &countReader{r: io.LimitReader(body, int64(c.maxBytes())+1), max: c.maxBytes()}
	br := bufio.NewReader(r)
	peek, err := br.Peek(1)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, ErrTooBig) {
		return Message{}, err
	}
	if len(peek) == 1 && peek[0] == '{' {
		b, rerr := io.ReadAll(br)
		if rerr != nil && !errors.Is(rerr, ErrTooBig) && !errors.Is(rerr, io.EOF) {
			return Message{}, rerr
		}
		if errors.Is(rerr, ErrTooBig) || r.n > r.max {
			return Message{}, ErrTooBig
		}
		return decodeJSON(b, emit)
	}
	var msg Message
	calls := map[int]*callAcc{}
	var order []int
	err = consumeSSE(br, func(data string) error {
		var ch sseChunk
		if err := json.Unmarshal([]byte(data), &ch); err != nil {
			return fmt.Errorf("openai: stream: %w", err)
		}
		if ch.Error != nil && ch.Error.Message != "" {
			return &APIError{Status: 200, Message: ch.Error.Message, key: c.APIKey}
		}
		if len(ch.Choices) == 0 {
			return nil
		}
		d := ch.Choices[0]
		if d.FinishReason != "" {
			msg.Finish = d.FinishReason
		}
		think := d.Delta.Reasoning
		if think == "" {
			think = d.Delta.Reason
		}
		if d.Delta.Content != "" || think != "" {
			msg.Content += d.Delta.Content
			if emit != nil {
				emit(Event{Content: d.Delta.Content, Thinking: think})
			}
		}
		for _, tc := range d.Delta.ToolCalls {
			acc := calls[tc.Index]
			if acc == nil {
				acc = &callAcc{}
				calls[tc.Index] = acc
				order = append(order, tc.Index)
			}
			acc.seen = true
			if tc.ID != "" {
				acc.id = tc.ID
			}
			if tc.Function.Name != "" {
				acc.name = tc.Function.Name
			}
			acc.args.WriteString(tc.Function.Arguments)
		}
		return nil
	})
	if err != nil {
		return Message{}, err
	}
	if r.n > r.max {
		return Message{}, ErrTooBig
	}
	for _, i := range order {
		acc := calls[i]
		msg.ToolCalls = append(msg.ToolCalls, ToolCall{ID: acc.id, Name: acc.name, Arguments: acc.args.String()})
	}
	return msg, nil
}

func decodeJSON(b []byte, emit func(Event)) (Message, error) {
	var parsed struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(b, &parsed); err != nil {
		return Message{}, fmt.Errorf("openai: response: %w", err)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return Message{}, &APIError{Status: 200, Message: parsed.Error.Message}
	}
	if len(parsed.Choices) == 0 {
		return Message{}, errors.New("openai: response has no choices")
	}
	ch := parsed.Choices[0]
	msg := Message{Content: ch.Message.Content, Finish: ch.FinishReason}
	for _, tc := range ch.Message.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments})
	}
	if emit != nil && msg.Content != "" {
		emit(Event{Content: msg.Content})
	}
	return msg, nil
}

var errDone = errors.New("sse done")

// consumeSSE reads one SSE stream. Data lines of one event are joined
// with newlines. Comment lines (":") are ignored. CRLF is a line ending.
func consumeSSE(br *bufio.Reader, fn func(data string) error) error {
	var data []string
	flush := func() error {
		if len(data) == 0 {
			return nil
		}
		s := strings.Join(data, "\n")
		data = nil
		if s == "[DONE]" {
			return errDone
		}
		return fn(s)
	}
	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			line = strings.TrimRight(line, "\r\n")
			switch {
			case line == "":
				if ferr := flush(); ferr != nil {
					if errors.Is(ferr, errDone) {
						return nil
					}
					return ferr
				}
			case strings.HasPrefix(line, ":"):
			case strings.HasPrefix(line, "data:"):
				rest := strings.TrimPrefix(line, "data:")
				rest = strings.TrimPrefix(rest, " ")
				data = append(data, rest)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				if ferr := flush(); ferr != nil && !errors.Is(ferr, errDone) {
					return ferr
				}
				return nil
			}
			return err
		}
	}
}

// countReader counts bytes and returns ErrTooBig once more than max have
// been read. A body of exactly max bytes is fine.
type countReader struct {
	r   io.Reader
	n   int
	max int
}

func (c *countReader) Read(p []byte) (int, error) {
	if c.n > c.max {
		return 0, ErrTooBig
	}
	n, err := c.r.Read(p)
	c.n += n
	if c.n > c.max {
		return n, ErrTooBig
	}
	return n, err
}

func (c *Client) scrub(err error) error {
	if err == nil || c.APIKey == "" || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrTooBig) || errors.Is(err, ErrBaseURL) {
		return err
	}
	msg := err.Error()
	if !strings.Contains(msg, c.APIKey) {
		return err
	}
	return errors.New(strings.ReplaceAll(msg, c.APIKey, "[redacted]"))
}
