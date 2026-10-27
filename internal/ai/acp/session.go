package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/Licoy/sshyard/internal/ai/policy"
	sdk "github.com/coder/acp-go-sdk"
)

// Config starts one agent process for one pane.
type Config struct {
	Command    string
	Args       []string
	Env        map[string]string // the agent's own variables; still filtered
	BaseEnv    []string          // KEY=VALUE. Nil uses os.Environ.
	Type       string            // codex, claude, grok, custom
	Gate       policy.Gate
	Files      Files
	Exec       policy.Exec
	RemoteHome string
	CwdFollow  func() string
	// OnUpdate is called from a background goroutine and must not block.
	OnUpdate func(Update)
}

// Resource is an embedded prompt block.
type Resource struct {
	URI  string
	Text string
}

// AuthChoice is one login method the agent offered.
type AuthChoice struct {
	ID   string
	Name string
	Kind string // agent, env_var, terminal
}

// AuthRequiredError means session/new refused with -32000 and at least
// one agent-type method. The Session is alive; call Authenticate, then
// Close when finished.
type AuthRequiredError struct {
	Methods []AuthChoice
}

func (e *AuthRequiredError) Error() string { return "authentication required" }

// Session is one agent process and one ACP connection.
type Session struct {
	cfg     Config
	gate    policy.Gate
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	conn    *sdk.ClientSideConnection
	sandbox string
	stderr  *ring

	stderrDone chan struct{}

	mu        sync.Mutex
	sid       sdk.SessionId
	methods   []AuthChoice
	authTried bool
	pinned    string
	bounced   bool
	modelID   string
	closing   bool
	perms     []context.CancelFunc
	terms     map[string]*termProc

	updates   chan sdk.SessionNotification
	closed    chan struct{}
	wg        sync.WaitGroup
	closeOnce sync.Once
}

// Start launches the agent, initializes protocol version 1, and opens a
// session. A version mismatch or a failed session/new closes the process.
// AuthRequiredError leaves it running.
func Start(ctx context.Context, cfg Config) (*Session, error) {
	if cfg.Command == "" {
		return nil, errors.New("agent command is required")
	}
	if cfg.Gate == nil {
		return nil, errors.New("gate is required")
	}
	sandbox, err := os.MkdirTemp("", "sshyard-acp-")
	if err != nil {
		return nil, err
	}
	s := &Session{
		cfg:     cfg,
		gate:    cfg.Gate,
		sandbox: sandbox,
		stderr:  &ring{cap: stderrCap},
		closed:  make(chan struct{}),
		updates: make(chan sdk.SessionNotification, 64),
		terms:   map[string]*termProc{},
	}
	if err := s.spawn(); err != nil {
		_ = os.RemoveAll(sandbox)
		return nil, err
	}
	if err := s.initialize(ctx); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.openSession(ctx); err != nil {
		var auth *AuthRequiredError
		if errors.As(err, &auth) {
			return s, err
		}
		s.Close()
		return nil, err
	}
	return s, nil
}

func (s *Session) spawn() error {
	base := s.cfg.BaseEnv
	if base == nil {
		base = os.Environ()
	}
	cmd := exec.Command(s.cfg.Command, s.cfg.Args...)
	cmd.Env = FilterEnv(base, s.cfg.Env)
	cmd.Dir = s.sandbox
	setGroup(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	s.cmd = cmd
	s.stdin = stdin
	s.stderrDone = make(chan struct{})
	s.conn = sdk.NewClientSideConnection(s, stdin, stdout)
	s.conn.SetLogger(slog.New(slog.DiscardHandler))
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer close(s.stderrDone)
		_, _ = io.Copy(s.stderr, stderr)
	}()
	s.wg.Add(2)
	go s.pump()
	go s.watch()
	return nil
}

// An agent line over 10MB makes the SDK scanner fail and close the
// connection. The writer may then block on the unread tail; Close kills
// the process after a short wait. A notification flood that overflows the
// SDK's 1024-entry queue does the same. Prompt returns either way.
func (s *Session) watch() {
	defer s.wg.Done()
	<-s.conn.Done()
	s.mu.Lock()
	closing := s.closing
	s.mu.Unlock()
	if closing {
		return
	}
	// Stdout can close before the stderr copy finishes. Wait so the tail
	// includes the crash text. Close unblocks this by killing the process.
	select {
	case <-s.stderrDone:
	case <-s.closed:
		return
	}
	s.cancelPerms()
	s.killTerms()
	tail := TrimTail(s.stderr.String(), crashTail)
	text := "agent process exited"
	if tail != "" {
		text += "\n" + tail
	}
	s.emit(Update{Kind: "error", Text: text})
}

func (s *Session) pump() {
	defer s.wg.Done()
	for {
		select {
		case <-s.closed:
			return
		case n := <-s.updates:
			s.handleUpdate(n)
		}
	}
}

func (s *Session) handleUpdate(n sdk.SessionNotification) {
	u, ok := decodeUpdate(n.Update)
	if !ok {
		return
	}
	if u.Kind == "mode" && SkipApproval(u.Mode) {
		s.bounceMode(u.Mode)
		return
	}
	if u.Kind == "config" && u.Config != "" {
		s.mu.Lock()
		s.modelID = u.Config
		s.mu.Unlock()
	}
	s.emit(u)
}

func (s *Session) bounceMode(id string) {
	s.mu.Lock()
	pinned := s.pinned
	stop := s.bounced || pinned == "" || SkipApproval(pinned)
	if !stop {
		s.bounced = true
	}
	sid := s.sid
	s.mu.Unlock()
	if stop {
		s.emit(Update{Kind: "warning", Text: "agent requested a mode that skips approval; session stopped"})
		_ = s.Stop()
		return
	}
	s.emit(Update{Kind: "warning", Text: "agent requested mode " + id + "; restored " + pinned})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := s.conn.SetSessionMode(ctx, sdk.SetSessionModeRequest{
		SessionId: sid,
		ModeId:    sdk.SessionModeId(pinned),
	}); err != nil {
		s.emit(Update{Kind: "error", Text: "could not restore session mode"})
		_ = s.Stop()
	}
}

func (s *Session) emit(u Update) {
	if s.cfg.OnUpdate != nil {
		s.cfg.OnUpdate(u)
	}
}

func initRequest() sdk.InitializeRequest {
	return sdk.InitializeRequest{
		ProtocolVersion: sdk.ProtocolVersionNumber,
		ClientCapabilities: sdk.ClientCapabilities{
			Fs:       sdk.FileSystemCapabilities{ReadTextFile: true, WriteTextFile: true},
			Terminal: true,
		},
	}
}

func (s *Session) initialize(ctx context.Context) error {
	resp, err := s.conn.Initialize(ctx, initRequest())
	if err != nil {
		return err
	}
	if resp.ProtocolVersion != sdk.ProtocolVersionNumber {
		return fmt.Errorf("%w: agent speaks %d", ErrProtocolVersion, resp.ProtocolVersion)
	}
	return nil
}

func newSessionRequest(cwd, kind string) sdk.NewSessionRequest {
	req := sdk.NewSessionRequest{Cwd: cwd, McpServers: []sdk.McpServer{}}
	if kind == "claude" {
		req.Meta = map[string]any{
			"claudeCode": map[string]any{
				"options": map[string]any{"allowDangerouslySkipPermissions": false},
			},
		}
	}
	return req
}

func (s *Session) openSession(ctx context.Context) error {
	resp, err := s.conn.NewSession(ctx, newSessionRequest(s.sandbox, s.cfg.Type))
	if err != nil {
		var re *sdk.RequestError
		if errors.As(err, &re) && re.Code == -32000 {
			return s.authRequired(re.Data)
		}
		return err
	}
	s.mu.Lock()
	s.sid = resp.SessionId
	if id := modelID(resp.ConfigOptions); id != "" {
		s.modelID = id
	}
	s.mu.Unlock()
	return s.applyMode(ctx, resp)
}

func (s *Session) authRequired(data any) error {
	choices, err := authChoices(data)
	if err != nil {
		return err
	}
	for _, c := range choices {
		if c.Kind != "agent" {
			return fmt.Errorf("%w: %s", ErrUnsupportedAuth, c.Kind)
		}
	}
	s.mu.Lock()
	s.methods = append([]AuthChoice(nil), choices...)
	s.mu.Unlock()
	return &AuthRequiredError{Methods: choices}
}

func authChoices(data any) ([]AuthChoice, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return nil, errors.New("authentication required")
	}
	var methods []sdk.AuthMethod
	if err := json.Unmarshal(b, &methods); err != nil || len(methods) == 0 {
		return nil, errors.New("authentication required")
	}
	out := make([]AuthChoice, 0, len(methods))
	for _, m := range methods {
		switch {
		case m.EnvVar != nil:
			out = append(out, AuthChoice{ID: m.EnvVar.Id, Name: m.EnvVar.Name, Kind: "env_var"})
		case m.Terminal != nil:
			out = append(out, AuthChoice{ID: m.Terminal.Id, Name: m.Terminal.Name, Kind: "terminal"})
		case m.Agent != nil:
			out = append(out, AuthChoice{ID: m.Agent.Id, Name: m.Agent.Name, Kind: "agent"})
		default:
			return nil, errors.New("authentication required")
		}
	}
	return out, nil
}

func (s *Session) applyMode(ctx context.Context, resp sdk.NewSessionResponse) error {
	var ids []string
	current := ""
	if resp.Modes != nil {
		current = string(resp.Modes.CurrentModeId)
		for _, m := range resp.Modes.AvailableModes {
			ids = append(ids, string(m.Id))
		}
	}
	mode, set, stop := PickMode(s.cfg.Type, current, ids)
	if stop {
		return errors.New("agent mode skips approval")
	}
	s.mu.Lock()
	if set {
		s.pinned = mode
	} else {
		s.pinned = current
	}
	sid := s.sid
	s.mu.Unlock()
	if !set {
		return nil
	}
	if _, err := s.conn.SetSessionMode(ctx, sdk.SetSessionModeRequest{
		SessionId: sid,
		ModeId:    sdk.SessionModeId(mode),
	}); err != nil {
		return fmt.Errorf("set session mode %s: %w", mode, err)
	}
	return nil
}

// Authenticate logs in with an agent-type method and retries session/new
// once. A second call fails.
func (s *Session) Authenticate(ctx context.Context, methodID string) error {
	s.mu.Lock()
	if s.sid != "" {
		s.mu.Unlock()
		return errors.New("session already open")
	}
	if s.authTried {
		s.mu.Unlock()
		return errors.New("authentication already attempted")
	}
	ok := false
	for _, m := range s.methods {
		if m.ID == methodID && m.Kind == "agent" {
			ok = true
		}
	}
	s.authTried = true
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnsupportedAuth, methodID)
	}
	if _, err := s.conn.Authenticate(ctx, sdk.AuthenticateRequest{MethodId: methodID}); err != nil {
		return err
	}
	return s.openSession(ctx)
}

// Prompt sends text and embedded resources. The stop reason is the
// agent's. Cancelling ctx cancels the turn, answers pending permission
// requests, and stops running terminals.
func (s *Session) Prompt(ctx context.Context, text string, resources []Resource) (string, error) {
	s.mu.Lock()
	sid := s.sid
	s.mu.Unlock()
	if sid == "" {
		return "", errors.New("session is not open")
	}
	blocks := make([]sdk.ContentBlock, 0, 1+len(resources))
	if text != "" {
		blocks = append(blocks, sdk.TextBlock(text))
	}
	for _, r := range resources {
		if r.URI == "" {
			return "", errInvalid
		}
		blocks = append(blocks, sdk.ResourceBlock(sdk.EmbeddedResourceResource{
			TextResourceContents: &sdk.TextResourceContents{Uri: r.URI, Text: r.Text},
		}))
	}
	if len(blocks) == 0 {
		return "", errors.New("empty prompt")
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			s.cancelPerms()
			s.killTerms()
		case <-done:
		case <-s.closed:
		}
	}()
	resp, err := s.conn.Prompt(ctx, sdk.PromptRequest{SessionId: sid, Prompt: blocks})
	if err != nil {
		return "", err
	}
	return string(resp.StopReason), nil
}

// SetModel selects the config option whose category is "model".
func (s *Session) SetModel(ctx context.Context, value string) error {
	s.mu.Lock()
	id := s.modelID
	sid := s.sid
	s.mu.Unlock()
	if id == "" {
		return errors.New("agent has no model option")
	}
	_, err := s.conn.SetSessionConfigOption(ctx, sdk.SetSessionConfigOptionRequest{
		ValueId: &sdk.SetSessionConfigOptionValueId{
			ConfigId:  sdk.SessionConfigId(id),
			SessionId: sid,
			Value:     sdk.SessionConfigValueId(value),
		},
	})
	return err
}

// Stop cancels the turn, answers every pending permission request with
// Cancelled, and stops running terminals. The process keeps running.
func (s *Session) Stop() error {
	s.cancelPerms()
	s.killTerms()
	s.mu.Lock()
	sid := s.sid
	conn := s.conn
	s.mu.Unlock()
	if sid == "" || conn == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return conn.Cancel(ctx, sdk.CancelNotification{SessionId: sid})
}

// Close cancels the process, closes its stdin, waits, then kills it and
// removes the sandbox. It is safe to call more than once.
func (s *Session) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closing = true
		s.mu.Unlock()
		s.cancelPerms()
		s.killTerms()
		if s.stdin != nil {
			_ = s.stdin.Close()
		}
		if s.cmd != nil {
			done := make(chan struct{})
			go func() {
				_ = s.cmd.Wait()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				killProc(s.cmd)
				<-done
			}
		}
		close(s.closed)
		s.wg.Wait()
		if s.sandbox != "" {
			_ = os.RemoveAll(s.sandbox)
		}
	})
	return nil
}

// Sandbox is the empty directory the agent was started in.
func (s *Session) Sandbox() string { return s.sandbox }

// Diagnostics is the bounded stderr tail. It is never parsed as protocol.
func (s *Session) Diagnostics() string { return s.stderr.String() }

func (s *Session) checkSession(id sdk.SessionId) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing || s.sid == "" || id != s.sid {
		return errSession
	}
	return nil
}

func (s *Session) mapPath(p string) (string, error) {
	return MapPath(s.sandbox, s.cfg.RemoteHome, p, s.cfg.CwdFollow)
}

func (s *Session) trackPerm(cancel context.CancelFunc) {
	s.mu.Lock()
	s.perms = append(s.perms, cancel)
	s.mu.Unlock()
}

func (s *Session) cancelPerms() {
	s.mu.Lock()
	cancels := s.perms
	s.perms = nil
	s.mu.Unlock()
	for _, c := range cancels {
		c()
	}
}
