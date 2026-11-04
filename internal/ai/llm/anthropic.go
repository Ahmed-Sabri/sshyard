package llm

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// This file is the Anthropic Messages format: POST {base}/messages. System
// messages become the top-level system prompt, tool results go back in a
// user message, and signed thinking blocks are replayed with the assistant
// message they belong to.

func (c *Client) anthropicPayload(req Request) ([]byte, error) {
	system, msgs := c.anthropicMessages(req.Messages, req.Model)
	n := c.maxTokens(req)
	if n <= 0 {
		n = AnthropicMaxTokens
	}
	body := map[string]any{
		"model":      req.Model,
		"messages":   msgs,
		"max_tokens": n,
		"stream":     true,
	}
	if system != "" {
		body["system"] = system
	}
	if c.Temperature != nil {
		body["temperature"] = *c.Temperature
	}
	if c.TopP != nil {
		body["top_p"] = *c.TopP
	}
	// Current models cannot turn thinking off; EffortNone leaves the default.
	if req.Effort != "" && req.Effort != EffortNone {
		body["thinking"] = map[string]any{"type": "adaptive", "display": "summarized"}
		body["output_config"] = map[string]any{"effort": req.Effort}
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, len(req.Tools))
		for i, t := range req.Tools {
			schema := json.RawMessage(t.Parameters)
			if len(schema) == 0 {
				schema = json.RawMessage(`{"type":"object"}`)
			}
			w := map[string]any{"name": t.Name, "input_schema": schema}
			if t.Description != "" {
				w["description"] = t.Description
			}
			tools[i] = w
		}
		body["tools"] = tools
	}
	return json.Marshal(body)
}

type antMsg struct {
	Role    string `json:"role"`
	Content []any  `json:"content"`
}

// anthropicMessages joins the system messages into one prompt and merges
// consecutive messages of one role, as tool results and the next question
// must share the user turn.
func (c *Client) anthropicMessages(in []Message, model string) (string, []antMsg) {
	var (
		system []string
		out    []antMsg
	)
	add := func(role string, blocks ...any) {
		if len(blocks) == 0 {
			return
		}
		if n := len(out); n > 0 && out[n-1].Role == role {
			out[n-1].Content = append(out[n-1].Content, blocks...)
			return
		}
		out = append(out, antMsg{Role: role, Content: blocks})
	}
	for _, m := range in {
		switch m.Role {
		case "system":
			if m.Content != "" {
				system = append(system, m.Content)
			}
		case "tool":
			b := map[string]any{"type": "tool_result", "tool_use_id": m.ToolCallID}
			if m.Content != "" {
				b["content"] = m.Content
			}
			add("user", b)
		case "assistant":
			var blocks []any
			if op := c.opaque(m, model); op != nil {
				var think []json.RawMessage
				if json.Unmarshal(op, &think) == nil {
					for _, t := range think {
						blocks = append(blocks, t)
					}
				}
			}
			if m.Content != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": m.Content})
			}
			for _, tc := range m.ToolCalls {
				input := json.RawMessage(tc.Arguments)
				if !json.Valid(input) {
					input = json.RawMessage(`{}`)
				}
				blocks = append(blocks, map[string]any{"type": "tool_use", "id": tc.ID, "name": tc.Name, "input": input})
			}
			add("assistant", blocks...)
		default:
			var blocks []any
			for _, p := range m.Parts {
				kind := "document"
				if p.IsImage() {
					kind = "image"
				}
				blocks = append(blocks, map[string]any{"type": kind, "source": map[string]any{"type": "base64", "media_type": p.MIME, "data": b64(p.Data)}})
			}
			if m.Content != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": m.Content})
			}
			add("user", blocks...)
		}
	}
	return strings.Join(system, "\n\n"), out
}

type antBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	Signature string          `json:"signature"`
	Data      string          `json:"data"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
}

type antEvent struct {
	Type         string    `json:"type"`
	Index        int       `json:"index"`
	ContentBlock *antBlock `json:"content_block"`
	Delta        *struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		Signature   string `json:"signature"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// antAcc is one content block as it streams.
type antAcc struct {
	block antBlock
	text  strings.Builder
	sig   strings.Builder
	input strings.Builder
}

func (c *Client) readAnthropic(br *bufioReader, emit func(Event)) (Message, error) {
	if br.JSON {
		b, err := br.all()
		if err != nil {
			return Message{}, err
		}
		var parsed struct {
			Content    []antBlock `json:"content"`
			StopReason string     `json:"stop_reason"`
			Error      *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(b, &parsed); err != nil {
			return Message{}, fmt.Errorf("llm: response: %w", err)
		}
		if parsed.Error != nil && parsed.Error.Message != "" {
			return Message{}, c.streamError(parsed.Error.Message)
		}
		msg, err := antMessage(parsed.Content, parsed.StopReason)
		if err == nil && emit != nil && msg.Content != "" {
			emit(Event{Content: msg.Content})
		}
		return msg, err
	}
	var (
		blocks  = map[int]*antAcc{}
		stop    string
		stopped bool
	)
	err := consumeSSE(br.Reader, func(data string) error {
		var ev antEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return fmt.Errorf("llm: stream: %w", err)
		}
		if stopped {
			return fmt.Errorf("%w: data after completion", ErrProtocol)
		}
		switch ev.Type {
		case "error":
			msg := "stream error"
			if ev.Error != nil && ev.Error.Message != "" {
				msg = ev.Error.Message
			}
			return c.streamError(msg)
		case "content_block_start":
			if ev.ContentBlock == nil || ev.Index < 0 || blocks[ev.Index] != nil {
				return fmt.Errorf("%w: bad content block", ErrProtocol)
			}
			acc := &antAcc{block: *ev.ContentBlock}
			acc.text.WriteString(acc.block.Text + acc.block.Thinking)
			acc.sig.WriteString(acc.block.Signature)
			blocks[ev.Index] = acc
			if emit != nil {
				switch {
				case acc.block.Type == "text" && acc.block.Text != "":
					emit(Event{Content: acc.block.Text})
				case acc.block.Type == "thinking" && acc.block.Thinking != "":
					emit(Event{Thinking: acc.block.Thinking})
				}
			}
		case "content_block_delta":
			acc := blocks[ev.Index]
			if acc == nil || ev.Delta == nil {
				return fmt.Errorf("%w: delta without block", ErrProtocol)
			}
			switch ev.Delta.Type {
			case "text_delta":
				acc.text.WriteString(ev.Delta.Text)
				if emit != nil && ev.Delta.Text != "" {
					emit(Event{Content: ev.Delta.Text})
				}
			case "thinking_delta":
				acc.text.WriteString(ev.Delta.Thinking)
				if emit != nil && ev.Delta.Thinking != "" {
					emit(Event{Thinking: ev.Delta.Thinking})
				}
			case "signature_delta":
				acc.sig.WriteString(ev.Delta.Signature)
			case "input_json_delta":
				acc.input.WriteString(ev.Delta.PartialJSON)
			}
		case "message_delta":
			if ev.Delta != nil && ev.Delta.StopReason != "" {
				stop = ev.Delta.StopReason
			}
		case "message_stop":
			stopped = true
		}
		return nil
	})
	if err != nil {
		return Message{}, err
	}
	if !stopped {
		return Message{}, fmt.Errorf("%w: missing or unsuccessful completion", ErrIncompleteResponse)
	}
	idx := make([]int, 0, len(blocks))
	for i := range blocks {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	content := make([]antBlock, 0, len(idx))
	for _, i := range idx {
		acc := blocks[i]
		b := acc.block
		switch b.Type {
		case "text":
			b.Text = acc.text.String()
		case "thinking":
			b.Thinking, b.Signature = acc.text.String(), acc.sig.String()
		case "tool_use":
			if s := strings.TrimSpace(acc.input.String()); s != "" {
				b.Input = json.RawMessage(s)
			}
		}
		content = append(content, b)
	}
	return antMessage(content, stop)
}

// antMessage builds the assistant message from its content blocks.
func antMessage(content []antBlock, stop string) (Message, error) {
	var (
		msg   Message
		think []any
	)
	for _, b := range content {
		switch b.Type {
		case "text":
			msg.Content += b.Text
		case "thinking":
			think = append(think, map[string]any{"type": "thinking", "thinking": b.Thinking, "signature": b.Signature})
		case "redacted_thinking":
			think = append(think, map[string]any{"type": "redacted_thinking", "data": b.Data})
		case "tool_use":
			args := strings.TrimSpace(string(b.Input))
			if args == "" || args == "null" {
				args = "{}"
			}
			msg.ToolCalls = append(msg.ToolCalls, ToolCall{ID: b.ID, Name: b.Name, Arguments: args})
		}
	}
	switch stop {
	case "end_turn", "stop_sequence", "refusal":
		msg.Finish = "stop"
	case "tool_use":
		msg.Finish = "tool_calls"
	case "max_tokens":
		msg.Finish = "length"
	}
	if err := validateMessage(msg); err != nil {
		return Message{}, err
	}
	if len(think) > 0 {
		msg.Opaque, _ = json.Marshal(think)
	}
	return msg, nil
}
