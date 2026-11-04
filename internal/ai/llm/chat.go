package llm

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// This file is the OpenAI Chat Completions format: POST {base}/chat/completions.

func (c *Client) maxTokens(req Request) int {
	if req.MaxTokens > 0 {
		return req.MaxTokens
	}
	return c.MaxTokens
}

func (c *Client) chatPayload(req Request) ([]byte, error) {
	body := map[string]any{
		"model":    req.Model,
		"messages": chatMessages(req.Messages),
		"stream":   true,
	}
	if c.Temperature != nil {
		body["temperature"] = *c.Temperature
	}
	if c.TopP != nil {
		body["top_p"] = *c.TopP
	}
	if n := c.maxTokens(req); n > 0 {
		if c.LegacyMaxTokens {
			body["max_tokens"] = n
		} else {
			body["max_completion_tokens"] = n
		}
	}
	if req.Effort != "" {
		body["reasoning_effort"] = req.Effort
	}
	if len(req.Tools) > 0 {
		body["tools"] = chatTools(req.Tools)
	}
	return json.Marshal(body)
}

type chatMsg struct {
	Role       string     `json:"role"`
	Content    any        `json:"content,omitempty"`
	ToolCalls  []chatCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type chatCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func chatMessages(in []Message) []chatMsg {
	out := make([]chatMsg, len(in))
	for i, m := range in {
		out[i] = chatMsg{Role: m.Role, ToolCallID: m.ToolCallID}
		switch {
		case len(m.Parts) > 0:
			out[i].Content = chatParts(m)
		case m.Content != "":
			out[i].Content = m.Content
		}
		for _, tc := range m.ToolCalls {
			w := chatCall{ID: tc.ID, Type: "function"}
			w.Function.Name = tc.Name
			w.Function.Arguments = tc.Arguments
			out[i].ToolCalls = append(out[i].ToolCalls, w)
		}
	}
	return out
}

func chatParts(m Message) []map[string]any {
	var out []map[string]any
	if m.Content != "" {
		out = append(out, map[string]any{"type": "text", "text": m.Content})
	}
	for _, p := range m.Parts {
		if p.IsImage() {
			out = append(out, map[string]any{"type": "image_url", "image_url": map[string]any{"url": dataURL(p)}})
			continue
		}
		out = append(out, map[string]any{"type": "file", "file": map[string]any{"filename": p.Name, "file_data": dataURL(p)}})
	}
	return out
}

func chatTools(in []Tool) []map[string]any {
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

type chatChunk struct {
	Choices []struct {
		Delta *struct {
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
}

func (c *Client) readChat(br *bufioReader, emit func(Event)) (Message, error) {
	if br.JSON {
		b, err := br.all()
		if err != nil {
			return Message{}, err
		}
		return c.decodeChatJSON(b, emit)
	}
	var msg Message
	calls := map[int]*callAcc{}
	var order []int
	err := consumeSSE(br.Reader, func(data string) error {
		var ch chatChunk
		if err := json.Unmarshal([]byte(data), &ch); err != nil {
			return fmt.Errorf("llm: stream: %w", err)
		}
		if ch.Error != nil && ch.Error.Message != "" {
			return c.streamError(ch.Error.Message)
		}
		if len(ch.Choices) == 0 {
			return nil
		}
		d := ch.Choices[0]
		if d.Delta == nil {
			return fmt.Errorf("%w: choice has no delta", ErrProtocol)
		}
		if msg.Finish != "" {
			return fmt.Errorf("%w: data after completion", ErrProtocol)
		}
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
			if tc.Index < 0 {
				return fmt.Errorf("%w: negative tool index", ErrProtocol)
			}
			acc := calls[tc.Index]
			if acc == nil {
				acc = &callAcc{}
				calls[tc.Index] = acc
				order = append(order, tc.Index)
			}
			if tc.ID != "" {
				if acc.id != "" && acc.id != tc.ID {
					return fmt.Errorf("%w: conflicting tool id", ErrProtocol)
				}
				acc.id = tc.ID
			}
			if tc.Function.Name != "" {
				if acc.name != "" && acc.name != tc.Function.Name {
					return fmt.Errorf("%w: conflicting tool name", ErrProtocol)
				}
				acc.name = tc.Function.Name
			}
			acc.args.WriteString(tc.Function.Arguments)
		}
		return nil
	})
	if err != nil {
		return Message{}, err
	}
	for _, i := range order {
		acc := calls[i]
		msg.ToolCalls = append(msg.ToolCalls, ToolCall{ID: acc.id, Name: acc.name, Arguments: acc.args.String()})
	}
	if err := validateMessage(msg); err != nil {
		return Message{}, err
	}
	return msg, nil
}

func (c *Client) decodeChatJSON(b []byte, emit func(Event)) (Message, error) {
	var parsed struct {
		Choices []struct {
			Message *struct {
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
		return Message{}, fmt.Errorf("llm: response: %w", err)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return Message{}, c.streamError(parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return Message{}, errors.New("llm: response has no choices")
	}
	ch := parsed.Choices[0]
	if ch.Message == nil {
		return Message{}, fmt.Errorf("%w: choice has no message", ErrProtocol)
	}
	msg := Message{Content: ch.Message.Content, Finish: ch.FinishReason}
	for _, tc := range ch.Message.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments})
	}
	if err := validateMessage(msg); err != nil {
		return Message{}, err
	}
	if emit != nil && msg.Content != "" {
		emit(Event{Content: msg.Content})
	}
	return msg, nil
}
