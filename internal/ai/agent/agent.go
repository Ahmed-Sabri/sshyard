// Package agent is SSHYard's built-in assistant: one host, one tool loop,
// every call checked by a policy.Gate. A Session is not safe for concurrent
// Turn and Continue; cancel with the context instead.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Licoy/sshyard/internal/ai/openai"
	"github.com/Licoy/sshyard/internal/ai/policy"
)

// MaxToolCalls is how many tool calls one turn runs before it pauses.
const MaxToolCalls = 25

// maxOutput is the most of one tool result kept for the timeline and the model.
const maxOutput = 1 << 20

// defaultMaxChars is the context budget. Oldest tool outputs are summarised past it.
const defaultMaxChars = 120_000

// ErrPaused is returned when a turn stops after MaxToolCalls tool calls.
// Continue resumes it.
var ErrPaused = errors.New("agent: paused after 25 tool calls")

var errTurnPaused = errors.New("agent: turn is paused")

// Streamer is the OpenAI-compatible completion client.
type Streamer interface {
	Stream(ctx context.Context, req openai.Request, emit func(openai.Event)) (openai.Message, error)
}

// Config builds a Session. A nil Tools uses Unavailable.
type Config struct {
	Model    Streamer
	Gate     policy.Gate
	Tools    Tools
	Redact   bool
	MaxChars int
}

// Kind is one timeline event.
type Kind string

const (
	EventText     Kind = "text"
	EventThinking Kind = "thinking"
	EventTool     Kind = "tool"
	EventPause    Kind = "pause"
	EventDone     Kind = "done"
)

// Event is one thing the UI timeline shows. Tool is set for EventTool.
type Event struct {
	Kind Kind
	Text string
	Tool *ToolCard
}

// ToolCard is one tool call on the timeline. Output is truncated to 1 MB.
// Exit is -1 when the action did not run.
type ToolCard struct {
	Phase    string // "open" while it runs, "done" when it has a result
	ID       string
	Name     string
	Category string
	Risk     string
	Where    string
	Command  string
	Path     string
	Dir      string
	Verdict  string // full, auto, reviewer, user, reject, timeout, cancel
	Refusal  string
	Output   string
	Diff     string
	Exit     int
	DurMS    int64
	Run      bool
}

// Session is one built-in agent conversation. History lives in memory.
type Session struct {
	model    Streamer
	gate     policy.Gate
	tools    Tools
	redact   bool
	maxChars int
	msgs     []openai.Message
	hostSent bool
	used     int
	limit    int
	paused   bool
	pending  []openai.ToolCall
}

// New starts a session with the system prompt. The host context is added
// on the first Turn.
func New(cfg Config) *Session {
	tools := cfg.Tools
	if tools == nil {
		tools = Unavailable{}
	}
	maxChars := cfg.MaxChars
	if maxChars <= 0 {
		maxChars = defaultMaxChars
	}
	return &Session{
		model:    cfg.Model,
		gate:     cfg.Gate,
		tools:    tools,
		redact:   cfg.Redact,
		maxChars: maxChars,
		msgs:     []openai.Message{{Role: "system", Content: SystemPrompt}},
	}
}

// Paused reports whether the session is waiting for Continue.
func (s *Session) Paused() bool { return s.paused }

// Turn sends one user message. terminal, when set, is attached as untrusted
// data. A paused session refuses until Continue.
func (s *Session) Turn(ctx context.Context, user string, meta HostMeta, terminal string, emit func(Event)) error {
	if s.paused {
		return errTurnPaused
	}
	if s.model == nil {
		return errors.New("agent: model is nil")
	}
	if s.gate == nil {
		return errors.New("agent: gate is nil")
	}
	if !s.hostSent {
		s.msgs = append(s.msgs, openai.Message{Role: "system", Content: s.mask(hostMessage(meta))})
		s.hostSent = true
	}
	text := user
	if terminal != "" {
		if text != "" {
			text += "\n\n"
		}
		text += WrapUntrusted("terminal", terminal)
	}
	s.msgs = append(s.msgs, openai.Message{Role: "user", Content: s.mask(text)})
	s.used = 0
	s.limit = MaxToolCalls
	s.pending = nil
	return s.loop(ctx, emit)
}

// Continue runs the tool calls that were held at the pause, then keeps going.
// It grants another MaxToolCalls. It does nothing when the session is not paused.
func (s *Session) Continue(ctx context.Context, emit func(Event)) error {
	if !s.paused {
		return nil
	}
	if s.model == nil || s.gate == nil {
		return errors.New("agent: not configured")
	}
	s.paused = false
	s.limit = s.used + MaxToolCalls
	if err := s.drain(ctx, emit); err != nil {
		return err
	}
	return s.loop(ctx, emit)
}

func (s *Session) loop(ctx context.Context, emit func(Event)) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		s.msgs = trimContext(s.msgs, s.maxChars)
		msg, err := s.model.Stream(ctx, s.request(), func(ev openai.Event) {
			if emit == nil {
				return
			}
			if ev.Content != "" {
				emit(Event{Kind: EventText, Text: ev.Content})
			}
			if ev.Thinking != "" {
				emit(Event{Kind: EventThinking, Text: ev.Thinking})
			}
		})
		if err != nil {
			return err
		}
		if msg.Role == "" {
			msg.Role = "assistant"
		}
		s.msgs = append(s.msgs, msg)
		if len(msg.ToolCalls) == 0 {
			if emit != nil {
				emit(Event{Kind: EventDone, Text: msg.Content})
			}
			return nil
		}
		s.pending = append([]openai.ToolCall(nil), msg.ToolCalls...)
		if err := s.drain(ctx, emit); err != nil {
			return err
		}
	}
}

func (s *Session) drain(ctx context.Context, emit func(Event)) error {
	for len(s.pending) > 0 {
		if err := ctx.Err(); err != nil {
			s.cancelRest("cancelled")
			return err
		}
		if s.used >= s.limit {
			s.paused = true
			if emit != nil {
				emit(Event{Kind: EventPause})
			}
			return ErrPaused
		}
		call := s.pending[0]
		s.pending = s.pending[1:]
		if err := s.execOne(ctx, call, emit); err != nil {
			return err
		}
		s.used++
	}
	return nil
}

func (s *Session) execOne(ctx context.Context, call openai.ToolCall, emit func(Event)) error {
	raw := strings.TrimSpace(call.Arguments)
	if !json.Valid([]byte(raw)) {
		raw = "{}"
	}
	fields := map[string]any{}
	_ = json.Unmarshal([]byte(raw), &fields)
	act := actionFor(call.Name, fields)

	diff := ""
	if call.Name == "write_file" {
		if err := ctx.Err(); err != nil {
			s.dropCall(call.ID)
			return err
		}
		content := asString(fields["content"])
		old, ok, err := s.tools.ReadFile(ctx, act.Path)
		if ctx.Err() != nil {
			s.dropCall(call.ID)
			return ctx.Err()
		}
		if err != nil {
			// A failed read must not become a new-file write: that would
			// pass a zero version and overwrite whatever is there.
			card := ToolCard{
				ID: call.ID, Name: call.Name, Where: string(act.Where), Path: act.Path,
				Phase: "done", Exit: 1, Output: err.Error(),
			}
			s.emitTool(emit, card)
			s.appendTool(call.ID, err.Error())
			return nil
		}
		if !ok {
			diff = content
		} else {
			diff = Unified(act.Path, old, content)
		}
	}

	verdict, err := s.gate.Authorize(ctx, act, diff)
	if err != nil {
		s.dropCall(call.ID)
		return err
	}
	card := ToolCard{
		ID:       call.ID,
		Name:     call.Name,
		Category: string(verdict.Category),
		Risk:     riskName(verdict.Risk),
		Where:    string(act.Where),
		Command:  act.Command,
		Path:     act.Path,
		Dir:      act.Dir,
		Verdict:  verdictCode(verdict),
		Diff:     diff,
		Run:      verdict.Run,
		Exit:     -1,
	}
	if !verdict.Run {
		reason := verdict.Refusal
		if reason == "" {
			reason = "rejected"
		}
		card.Phase = "done"
		card.Refusal = reason
		card.Output = reason
		s.emitTool(emit, card)
		s.appendTool(call.ID, reason)
		if verdict.Answer == policy.Cancelled || ctx.Err() != nil {
			s.cancelRest("cancelled")
			if err := ctx.Err(); err != nil {
				return err
			}
			return context.Canceled
		}
		return nil
	}
	if err := ctx.Err(); err != nil {
		s.dropCall(call.ID)
		return err
	}
	card.Phase = "open"
	s.emitTool(emit, card)

	start := time.Now()
	out, exitCode, callErr := s.tools.Call(ctx, call.Name, json.RawMessage(raw))
	dur := time.Since(start).Milliseconds()
	if callErr != nil && (ctx.Err() != nil || errors.Is(callErr, context.Canceled) || errors.Is(callErr, context.DeadlineExceeded)) {
		// Stop, Stop all, and the runner timeout share this path; the command already started.
		s.gate.Done(act, verdict, -1, dur, countedBytes(s.tools, out))
		s.dropCall(call.ID)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return callErr
	}
	if callErr != nil && out == "" {
		out = callErr.Error()
		if exitCode == 0 {
			exitCode = 1
		}
	}
	s.gate.Done(act, verdict, exitCode, dur, countedBytes(s.tools, out))
	shown := truncOut(out)
	card.Phase = "done"
	card.Output = shown
	card.Exit = exitCode
	card.DurMS = dur
	s.emitTool(emit, card)
	s.appendTool(call.ID, shown)
	return nil
}

// countedBytes is the audit output size. Tools that ran a command report
// the CountingWriter total (negative means "not a command"); other tools
// use the text the model sees.
func countedBytes(t Tools, out string) int64 {
	type counter interface{ OutputBytes() int64 }
	if c, ok := t.(counter); ok {
		if n := c.OutputBytes(); n >= 0 {
			return n
		}
	}
	return int64(len(out))
}

func (s *Session) emitTool(emit func(Event), card ToolCard) {
	if emit == nil {
		return
	}
	emit(Event{Kind: EventTool, Tool: &card})
}

func (s *Session) appendTool(id, content string) {
	s.msgs = append(s.msgs, openai.Message{Role: "tool", ToolCallID: id, Content: s.mask(content)})
}

func (s *Session) dropCall(id string) {
	s.appendTool(id, "cancelled")
	s.cancelRest("cancelled")
}

func (s *Session) cancelRest(reason string) {
	for _, c := range s.pending {
		s.appendTool(c.ID, reason)
	}
	s.pending = nil
}

func (s *Session) mask(text string) string {
	if s.redact {
		return policy.Redact(text)
	}
	return text
}

func (s *Session) request() openai.Request {
	msgs := make([]openai.Message, len(s.msgs))
	for i, m := range s.msgs {
		msgs[i] = m
		if len(m.ToolCalls) > 0 {
			msgs[i].ToolCalls = append([]openai.ToolCall(nil), m.ToolCalls...)
		}
		if s.redact {
			msgs[i].Content = policy.Redact(m.Content)
			for j := range msgs[i].ToolCalls {
				msgs[i].ToolCalls[j].Arguments = policy.Redact(msgs[i].ToolCalls[j].Arguments)
			}
		}
	}
	return openai.Request{Messages: msgs, Tools: ToolDefs()}
}

func actionFor(name string, m map[string]any) policy.Action {
	a := policy.Action{Where: policy.Remote, Tool: name, Reason: asString(m["reason"])}
	switch name {
	case "run_command":
		a.Command = asString(m["command"])
		a.Dir = asString(m["dir"])
	default:
		a.Path = asString(m["path"])
	}
	return a
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func verdictCode(v policy.Verdict) string {
	if !v.Run {
		switch v.Answer {
		case policy.TimedOut:
			return "timeout"
		case policy.Cancelled:
			return "cancel"
		default:
			return "reject"
		}
	}
	switch v.By {
	case policy.ByFull:
		return "full"
	case policy.ByReviewer:
		return "reviewer"
	case policy.ByUser:
		return "user"
	default:
		return "auto"
	}
}

func riskName(r policy.Risk) string {
	switch r {
	case policy.RiskReadOnly:
		return "readonly"
	case policy.RiskLow:
		return "low"
	case policy.RiskMedium:
		return "medium"
	case policy.RiskHigh:
		return "high"
	case policy.RiskCritical:
		return "critical"
	default:
		return "unknown"
	}
}

func truncOut(s string) string {
	if len(s) <= maxOutput {
		return s
	}
	return s[:maxOutput] + "\n…"
}

func trimContext(msgs []openai.Message, max int) []openai.Message {
	if max <= 0 {
		max = defaultMaxChars
	}
	for messageSize(msgs) > max {
		idx := -1
		for i, m := range msgs {
			if m.Role == "tool" && !strings.HasPrefix(m.Content, "omitted tool output") {
				idx = i
				break
			}
		}
		if idx < 0 {
			break
		}
		body := msgs[idx].Content
		line, _, _ := strings.Cut(body, "\n")
		if len(line) > 80 {
			line = line[:80]
		}
		msgs[idx].Content = fmt.Sprintf("omitted tool output (%d bytes): %s", len(body), line)
	}
	return msgs
}

func messageSize(msgs []openai.Message) int {
	n := 0
	for _, m := range msgs {
		n += len(m.Content)
		for _, c := range m.ToolCalls {
			n += len(c.Name) + len(c.Arguments)
		}
	}
	return n
}
