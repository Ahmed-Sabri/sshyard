package app

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Licoy/sshyard/internal/ai/agent"
	"github.com/Licoy/sshyard/internal/ai/audit"
	"github.com/Licoy/sshyard/internal/ai/openai"
	"github.com/Licoy/sshyard/internal/ai/policy"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/vt"
)

// aiState is one AI pane's session. The timeline is touched on the UI
// thread only; resolved is the mode the gate reads, under mu.
type aiState struct {
	agent    string
	provider string
	link     int
	mode     string // pane override; empty inherits
	mu       sync.Mutex
	resolved policy.Mode

	sess     *agent.Session
	box      *modelBox
	cancel   context.CancelFunc
	running  bool
	paused   bool
	items    []aiItem
	input    string
	gen      int
	pending  *aiPend
	onlyDiff bool
	modeIdx  int
	tools    agent.Tools
}

type aiItem struct {
	kind string // user, text, think, tool, err
	text string
	tool agent.ToolCard
	open bool // thinking stays folded until the user opens it
}

type aiPend struct {
	prompt policy.Prompt
	ch     chan policy.Answer
}

type fullAsk struct {
	kind string // pane, settings, host
	conn int
	tick bool
	open bool
}

type consentAsk struct {
	host string
	conn int
	text string
	open bool
}

// modelBox is the client the session calls. The UI thread replaces s
// when the provider changes; Stream reads it under mu.
type modelBox struct {
	mu sync.Mutex
	s  agent.Streamer
}

func (b *modelBox) Stream(ctx context.Context, req openai.Request, emit func(openai.Event)) (openai.Message, error) {
	b.mu.Lock()
	s := b.s
	b.mu.Unlock()
	if s == nil {
		return openai.Message{}, errAIProvider
	}
	return s.Stream(ctx, req, emit)
}

var (
	errAIKey      = errors.New("ai: no api key")
	errAIProvider = errors.New("ai: no provider")
)

// OpenAI opens an assistant for hostID. link is a terminal pane to attach,
// or 0. It does not open an SSH connection.
func (a *App) OpenAI(hostID string, at openAt, link int) (int, error) {
	c, err := a.makeConn(hostID, layout.AI, link)
	if err != nil {
		return 0, err
	}
	a.place(c, at)
	a.aiRecent[hostID] = c.id
	return c.id, nil
}

// OpenAIBeside splits an assistant next to the focused pane, on its host.
// The link is that pane when it is a terminal.
func (a *App) OpenAIBeside() {
	t := a.ws.ActiveTab()
	cn := a.focused()
	if t == nil || cn == nil {
		return
	}
	link := 0
	if cn.kind == layout.Terminal {
		link = cn.id
	}
	a.OpenAI(cn.host.ID, openAt{pane: t.Focus, edge: layout.Right}, link)
}

// OpenAIBelow splits an assistant under the focused pane.
func (a *App) OpenAIBelow() {
	t := a.ws.ActiveTab()
	cn := a.focused()
	if t == nil || cn == nil {
		return
	}
	link := 0
	if cn.kind == layout.Terminal {
		link = cn.id
	}
	a.OpenAI(cn.host.ID, openAt{pane: t.Focus, edge: layout.Bottom}, link)
}

func (a *App) refreshHost(c *conn) {
	if h, ok := a.host(c.host.ID); ok {
		c.host = h
	}
}

func (a *App) refreshMode(c *conn) {
	if c == nil || c.ai == nil {
		return
	}
	a.refreshHost(c)
	m := policy.Resolve(policy.Mode(a.settings.AIDefaultMode), policy.Mode(c.host.AIPolicy.Mode), policy.Mode(c.ai.mode))
	c.ai.mu.Lock()
	c.ai.resolved = m
	c.ai.mu.Unlock()
}

func (a *App) modeOf(c *conn) policy.Mode {
	if c == nil || c.ai == nil {
		return policy.ModeAsk
	}
	c.ai.mu.Lock()
	defer c.ai.mu.Unlock()
	if c.ai.resolved == "" {
		return policy.ModeAsk
	}
	return c.ai.resolved
}

func (a *App) providerByID(id string) *settings.AIProvider {
	for i := range a.settings.AIProviders {
		if a.settings.AIProviders[i].ID == id {
			return &a.settings.AIProviders[i]
		}
	}
	return nil
}

func (a *App) aiClient(c *conn) (*openai.Client, error) {
	p := a.providerByID(c.ai.provider)
	if p == nil || p.Model == "" || p.BaseURL == "" {
		return nil, errAIProvider
	}
	b, err := a.kc.Get(keychain.AIKey(p.ID))
	if err != nil || len(bytes.TrimSpace(b)) == 0 {
		return nil, errAIKey
	}
	hdr := map[string]string{}
	for k, v := range p.Headers {
		hdr[k] = v
	}
	return &openai.Client{
		BaseURL: p.BaseURL, APIKey: string(b), Model: p.Model,
		Headers: hdr, Temperature: p.Temperature, MaxTokens: p.MaxTokens,
	}, nil
}

func (a *App) ensureAI(c *conn) error {
	if c.ai.sess != nil {
		return nil
	}
	a.refreshMode(c)
	var streamer agent.Streamer
	if a.newModel != nil {
		streamer = a.newModel(c)
		if streamer == nil {
			return errAIProvider
		}
	} else {
		cl, err := a.aiClient(c)
		if err != nil {
			return err
		}
		box := &modelBox{s: cl}
		c.ai.box = box
		streamer = box
	}
	var tools agent.Tools
	if a.newTools != nil {
		tools = a.newTools(c)
	}
	if tools == nil {
		tools = newHostTools(a, c)
	}
	c.ai.tools = tools
	mode := func() policy.Mode { return a.modeOf(c) }
	ask := aiPrompter{a: a, id: c.id}
	var gate policy.Gate
	if a.newGate != nil {
		gate = a.newGate(mode, ask)
	} else {
		var rev policy.Reviewer
		if a.newModel == nil {
			rev = aiReviewer{ask: a.reviewAsk(c)}
		}
		var aud policy.Auditor
		if a.p.DataDir != "" {
			if a.audit == nil {
				a.audit = audit.New(a.p.DataDir)
			}
			aud = a.audit
		}
		gate = policy.NewGate(policy.GateConfig{
			Mode:      mode,
			Scope:     a.aiScope(c),
			Prompter:  ask,
			Reviewer:  rev,
			Auditor:   aud,
			SessionID: strconv.Itoa(c.id),
			PaneID:    strconv.Itoa(c.id),
			Agent:     c.ai.agent,
			Host:      c.host.ID,
			HostMeta:  c.host.Label(),
			Output:    func() string { return a.aiOutput(c) },
		})
	}
	c.ai.sess = agent.New(agent.Config{Model: streamer, Gate: gate, Tools: tools, Redact: a.settings.AIRedact})
	return nil
}

func (a *App) aiScope(c *conn) policy.Scope {
	return policy.Scope{
		WritableRoots: append([]string(nil), c.host.AIPolicy.WritableRoots...),
		Sensitive:     append([]string(nil), c.host.AIPolicy.Sensitive...),
	}
}

// reviewSystem is the review model's instruction. It is not shown in the UI.
const reviewSystem = "Reply with only a JSON object {\"decision\":\"allow\"|\"ask\",\"reason\":\"short\"}. Do not call tools. Use allow only when the action is clearly safe; otherwise ask."

// aiReviewer is policy.Reviewer. A reply that is not the review JSON is an
// error, so the gate asks the user instead of treating it as a rejection.
type aiReviewer struct {
	ask func(ctx context.Context, prompt string) (string, error)
}

func (r aiReviewer) Review(ctx context.Context, in policy.ReviewInput) (bool, string, error) {
	if r.ask == nil {
		return false, "", errors.New("review is unavailable")
	}
	raw, err := r.ask(ctx, policy.BuildReviewPrompt(in))
	if err != nil {
		return false, "", err
	}
	allow, reason, ok := policy.ParseReview(raw)
	if !ok {
		return false, "", errors.New("unparsable review")
	}
	return allow, reason, nil
}

func (a *App) reviewAsk(c *conn) func(context.Context, string) (string, error) {
	return func(ctx context.Context, prompt string) (string, error) {
		box := c.ai.box
		if box == nil {
			return "", errAIProvider
		}
		msg, err := box.Stream(ctx, openai.Request{
			MaxTokens: 200,
			Messages: []openai.Message{
				{Role: "system", Content: reviewSystem},
				{Role: "user", Content: prompt},
			},
		}, nil)
		if err != nil {
			return "", err
		}
		return msg.Content, nil
	}
}

// aiOutput is the linked terminal text, read on the UI thread.
func (a *App) aiOutput(c *conn) string {
	ch := make(chan string, 1)
	a.post(func() { ch <- a.aiTerminal(c) })
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case s := <-ch:
		return s
	case <-timer.C:
		return ""
	}
}

// SendAI asks the pane's agent. Nothing is sent until the host's first-use
// notice has been confirmed.
func (a *App) SendAI(id int, text string) {
	text = strings.TrimSpace(text)
	c := a.conns[id]
	if c == nil || c.ai == nil || text == "" || c.ai.running {
		return
	}
	if !a.consented(c.host.ID) {
		a.consent = &consentAsk{host: c.host.ID, conn: id, text: text, open: true}
		return
	}
	if err := a.ensureAI(c); err != nil {
		c.ai.items = append(c.ai.items, aiItem{kind: "user", text: text}, aiItem{kind: "err", text: aiErrText(err)})
		return
	}
	a.aiRecent[c.host.ID] = c.id
	c.ai.items = append(c.ai.items, aiItem{kind: "user", text: text})
	c.ai.input = ""
	c.ai.gen++
	meta := a.aiMeta(c)
	term := a.aiTerminal(c)
	a.launch(c, func(ctx context.Context, emit func(agent.Event)) error {
		return c.ai.sess.Turn(ctx, text, meta, term, emit)
	})
}

// ContinueAI resumes a turn that paused after 25 tool calls.
func (a *App) ContinueAI(id int) {
	c := a.conns[id]
	if c == nil || c.ai == nil || c.ai.sess == nil || !c.ai.paused || c.ai.running {
		return
	}
	a.launch(c, func(ctx context.Context, emit func(agent.Event)) error {
		return c.ai.sess.Continue(ctx, emit)
	})
}

func (a *App) launch(c *conn, run func(context.Context, func(agent.Event)) error) {
	if c.ai.box != nil && a.newModel == nil {
		if cl, err := a.aiClient(c); err == nil {
			c.ai.box.mu.Lock()
			c.ai.box.s = cl
			c.ai.box.mu.Unlock()
		}
	}
	a.refreshMode(c)
	ctx, cancel := context.WithCancel(context.Background())
	c.ai.cancel = cancel
	c.ai.running = true
	c.ai.paused = false
	id := c.id
	tools := c.ai.tools
	if h, ok := tools.(*hostTools); ok {
		h.snap()
	}
	go func() {
		// end before onAIDone: the next SendAI waits on running, and must
		// not dial while this turn is still releasing the connection.
		err := func() error {
			if h, ok := tools.(*hostTools); ok {
				h.noteTurn(ctx)
				defer h.end()
			}
			return run(ctx, func(ev agent.Event) {
				a.post(func() { a.onAIEvent(id, ev) })
			})
		}()
		a.post(func() { a.onAIDone(id, err) })
	}()
}

func (a *App) onAIEvent(id int, ev agent.Event) {
	c := a.conns[id]
	if c == nil || c.ai == nil {
		return
	}
	switch ev.Kind {
	case agent.EventText:
		c.ai.items = appendChunk(c.ai.items, "text", ev.Text)
	case agent.EventThinking:
		c.ai.items = appendChunk(c.ai.items, "think", ev.Text)
	case agent.EventTool:
		if ev.Tool == nil {
			return
		}
		for i := range c.ai.items {
			if c.ai.items[i].kind == "tool" && c.ai.items[i].tool.ID == ev.Tool.ID {
				c.ai.items[i].tool = *ev.Tool
				return
			}
		}
		c.ai.items = append(c.ai.items, aiItem{kind: "tool", tool: *ev.Tool})
	case agent.EventPause:
		c.ai.paused = true
	case agent.EventDone:
		if ev.Text != "" && trailingText(c.ai.items) != ev.Text {
			c.ai.items = append(c.ai.items, aiItem{kind: "text", text: ev.Text})
		}
	}
}

func (a *App) onAIDone(id int, err error) {
	c := a.conns[id]
	if c == nil || c.ai == nil {
		return
	}
	c.ai.running = false
	c.ai.cancel = nil
	if err == nil {
		c.ai.paused = c.ai.sess != nil && c.ai.sess.Paused()
		return
	}
	if errors.Is(err, agent.ErrPaused) {
		c.ai.paused = true
		return
	}
	if errors.Is(err, context.Canceled) {
		return
	}
	c.ai.items = append(c.ai.items, aiItem{kind: "err", text: a.scrubAI(c, err)})
}

func (a *App) scrubAI(c *conn, err error) string {
	msg := err.Error()
	if c.ai != nil && c.ai.provider != "" {
		if b, e := a.kc.Get(keychain.AIKey(c.ai.provider)); e == nil && len(b) > 0 {
			msg = strings.ReplaceAll(msg, string(b), "[redacted]")
		}
	}
	return msg
}

func aiErrText(err error) string {
	switch {
	case errors.Is(err, errAIKey):
		return "key"
	case errors.Is(err, errAIProvider):
		return "provider"
	default:
		return err.Error()
	}
}

func appendChunk(items []aiItem, kind, text string) []aiItem {
	if text == "" {
		return items
	}
	if n := len(items); n > 0 && items[n-1].kind == kind {
		items[n-1].text += text
		return items
	}
	return append(items, aiItem{kind: kind, text: text})
}

func trailingText(items []aiItem) string {
	start := len(items)
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].kind != "text" {
			break
		}
		start = i
	}
	var b strings.Builder
	for _, it := range items[start:] {
		b.WriteString(it.text)
	}
	return b.String()
}

// StopAI cancels the pane's turn and any confirmation it is waiting on.
func (a *App) StopAI(id int) {
	c := a.conns[id]
	if c == nil || c.ai == nil {
		return
	}
	if c.ai.cancel != nil {
		c.ai.cancel()
	}
	if p := c.ai.pending; p != nil {
		c.ai.pending = nil
		select {
		case p.ch <- policy.Cancelled:
		default:
		}
	}
}

// StopAllAI stops every AI pane.
func (a *App) StopAllAI() {
	for _, c := range a.conns {
		if c.kind == layout.AI {
			a.StopAI(c.id)
		}
	}
}

// stopShortcut is ⌘. / Ctrl+.. Pending sheets close as cancelled and are
// not applied. A focused AI pane stops itself; otherwise the recent AI
// pane on the focused host stops.
func (a *App) stopShortcut() {
	a.consent = nil
	a.full = nil
	for _, c := range a.conns {
		if c.kind == layout.AI && c.ai != nil && c.ai.pending != nil {
			a.answerAI(c, policy.Cancelled)
		}
	}
	if cn := a.focused(); cn != nil && cn.kind == layout.AI {
		a.StopAI(cn.id)
		return
	}
	if cn := a.focused(); cn != nil {
		if id := a.aiRecent[cn.host.ID]; id != 0 {
			a.StopAI(id)
		}
	}
}

// InsertAI writes text into the linked terminal's input, without Enter.
// Control characters, including CR and LF, are removed first.
func (a *App) InsertAI(id int, text string) {
	c := a.conns[id]
	if c == nil || c.ai == nil {
		return
	}
	t := a.conns[c.ai.link]
	if t == nil || t.tv == nil {
		return
	}
	t.tv.send([]byte(stripInsert(text)))
}

func stripInsert(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

// AskAI sends selection to the AI pane last used on that host, or splits
// a new one beside from. The selection is marked untrusted.
func (a *App) AskAI(from *conn, selection string) {
	if from == nil || strings.TrimSpace(selection) == "" {
		return
	}
	id := a.aiRecent[from.host.ID]
	c := a.conns[id]
	if c == nil || c.kind != layout.AI || c.host.ID != from.host.ID {
		pane := 0
		if _, n := a.ws.Locate(from.id); n != nil {
			pane = n.ID
		}
		nid, err := a.OpenAI(from.host.ID, openAt{pane: pane, edge: layout.Right}, from.id)
		if err != nil {
			return
		}
		c = a.conns[nid]
	} else if c.ai != nil && c.ai.link == 0 && from.kind == layout.Terminal {
		c.ai.link = from.id
	}
	a.SendAI(c.id, agent.WrapUntrusted("selection", selection))
}

func (a *App) consented(hostID string) bool {
	for _, id := range a.settings.AIConsentHosts {
		if id == hostID {
			return true
		}
	}
	return false
}

func (a *App) grantConsent(hostID string) {
	if a.consented(hostID) {
		return
	}
	s := a.settings
	s.AIConsentHosts = append(append([]string{}, s.AIConsentHosts...), hostID)
	_ = a.ApplySettings(s)
}

func (a *App) aiMeta(c *conn) agent.HostMeta {
	a.refreshHost(c)
	cwd := ""
	if t := a.conns[c.ai.link]; t != nil {
		cwd = t.cwd
	}
	return agent.HostMeta{Name: c.host.Label(), Address: c.host.Addr(), User: c.host.User, Cwd: cwd}
}

func (a *App) aiTerminal(c *conn) string {
	t := a.conns[c.ai.link]
	if t == nil || t.kind != layout.Terminal || t.s == nil || t.s.Term == nil {
		return ""
	}
	text := t.s.Term.Text(vt.Pos{}, vt.Pos{Line: 1 << 30, Col: 1 << 20})
	lines := strings.Split(text, "\n")
	n := a.settings.AIContextLines
	if n < 1 {
		n = 200
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

func (a *App) setPaneMode(c *conn, mode string) {
	if c == nil || c.ai == nil {
		return
	}
	if mode == "full" && c.ai.mode != "full" {
		a.openFull("pane", c.id)
		return
	}
	c.ai.mode = mode
	a.refreshMode(c)
	a.saveLayout()
}

func (a *App) openFull(kind string, conn int) {
	a.full = &fullAsk{kind: kind, conn: conn, open: true}
}

func (a *App) applyFull() {
	f := a.full
	if f == nil {
		return
	}
	switch f.kind {
	case "pane":
		if c := a.conns[f.conn]; c != nil && c.ai != nil {
			c.ai.mode = "full"
			a.refreshMode(c)
			a.saveLayout()
		}
	case "settings":
		s := a.settings
		s.AIDefaultMode = "full"
		_ = a.ApplySettings(s)
		for _, c := range a.conns {
			a.refreshMode(c)
		}
	case "host":
		if a.form != nil {
			a.form.fullOK = true
			a.form.aiIdx = 3
			a.form.aiPrev = 3
		}
	}
}

// aiPrompter is policy.Prompter. Ask blocks the agent goroutine, never the UI thread.
type aiPrompter struct {
	a  *App
	id int
}

func (p aiPrompter) Ask(ctx context.Context, pr policy.Prompt) policy.Answer {
	if ctx.Err() != nil {
		return policy.Cancelled
	}
	ch := make(chan policy.Answer, 1)
	p.a.post(func() {
		c := p.a.conns[p.id]
		if c == nil || c.ai == nil {
			select {
			case ch <- policy.Cancelled:
			default:
			}
			return
		}
		c.ai.pending = &aiPend{prompt: pr, ch: ch}
	})
	select {
	case ans := <-ch:
		return ans
	case <-ctx.Done():
		p.a.post(func() {
			c := p.a.conns[p.id]
			if c != nil && c.ai != nil && c.ai.pending != nil && c.ai.pending.ch == ch {
				c.ai.pending = nil
			}
		})
		return policy.Cancelled
	}
}

func (a *App) answerAI(c *conn, ans policy.Answer) {
	if c == nil || c.ai == nil || c.ai.pending == nil {
		return
	}
	p := c.ai.pending
	c.ai.pending = nil
	select {
	case p.ch <- ans:
	default:
	}
}

func (a *App) pendingAI() *conn {
	if cn := a.focused(); cn != nil && cn.ai != nil && cn.ai.pending != nil {
		return cn
	}
	var best *conn
	for _, c := range a.conns {
		if c.ai == nil || c.ai.pending == nil {
			continue
		}
		if best == nil || c.id < best.id {
			best = c
		}
	}
	return best
}
