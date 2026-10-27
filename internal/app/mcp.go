package app

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Licoy/sshyard/internal/ai/audit"
	"github.com/Licoy/sshyard/internal/ai/policy"
	"github.com/Licoy/sshyard/internal/ai/remote"
	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/mcp"
	"github.com/Licoy/sshyard/internal/session"
	"golang.org/x/crypto/ssh"
)

// mcpState is the MCP endpoint and the SSH connections agents opened
// through it. srv, err and token belong to the UI thread; the sessions,
// which the tools change on the server's goroutines, are under mu.
type mcpState struct {
	srv   *mcp.Server
	err   error
	token string

	mu       sync.Mutex
	sessions []*mcpSession
	next     int
}

// mcpSession is one connection an agent opened with connect. It holds a
// reference on the host's pooled connection until it is closed.
type mcpSession struct {
	id      string
	host    assets.Host
	client  *ssh.Client
	release func()
	since   time.Time
}

// mcpAsk is the risk notice shown before the endpoint is turned on. at
// is when it first showed; confirming waits mcpWait from then.
type mcpAsk struct {
	open bool
	at   time.Time
}

const (
	// mcpWait is how long the risk notice shows before it can be confirmed.
	mcpWait = 3 * time.Second
	// mcpMaxSessions bounds the connections agents hold at once.
	mcpMaxSessions = 16
	// mcpTimeout is a command's limit unless the agent asks for another,
	// within remote's bounds.
	mcpTimeout = 60 * time.Second
)

// errMCPNeedsInput refuses a host that logs in with something the user
// types: the endpoint never asks for a secret, nor hands one out.
var errMCPNeedsInput = errors.New("this host needs a password or passphrase typed in SSHYard; MCP never asks for secrets, so the connection was refused")

// syncMCP starts or stops the endpoint as the settings say, on the
// settings' port. Restarting drops every agent connection.
func (a *App) syncMCP() {
	a.stopMCP()
	if !a.settings.MCPEnabled {
		return
	}
	tok, err := a.mcpToken()
	if err == nil {
		a.mcp.token = tok
		a.mcp.srv, err = mcp.Listen(a.settings.MCPPort, a.mcpHandler(tok))
	}
	a.mcp.err = err
}

// stopMCP closes the endpoint, which cancels the commands running, and
// the agents' connections.
func (a *App) stopMCP() {
	if a.mcp.srv != nil {
		a.mcp.srv.Close()
		a.mcp.srv = nil
	}
	a.mcp.err = nil
	a.mcp.mu.Lock()
	ss := a.mcp.sessions
	a.mcp.sessions = nil
	a.mcp.mu.Unlock()
	for _, s := range ss {
		s.release()
	}
}

// mcpToken is the endpoint's bearer token, made the first time.
func (a *App) mcpToken() (string, error) {
	if b, err := a.kc.Get(keychain.MCPToken); err == nil && len(b) > 0 {
		return string(b), nil
	} else if err != nil && !errors.Is(err, keychain.ErrNotFound) {
		return "", err
	}
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	tok := "sshyard_" + base64.RawURLEncoding.EncodeToString(b[:])
	return tok, a.kc.Set(keychain.MCPToken, []byte(tok))
}

// newMCPToken replaces the token; agents holding the old one are cut off.
func (a *App) newMCPToken() error {
	if err := a.kc.Delete(keychain.MCPToken); err != nil && !errors.Is(err, keychain.ErrNotFound) {
		return err
	}
	a.syncMCP()
	return a.mcp.err
}

// setMCP turns the endpoint on or off and saves it.
func (a *App) setMCP(on bool) error {
	s := a.settings
	s.MCPEnabled = on
	return a.ApplySettings(s)
}

// mcpConfig is what an agent's MCP settings need to reach the endpoint.
func (a *App) mcpConfig() string {
	if a.mcp.srv == nil {
		return ""
	}
	b, _ := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{"sshyard": map[string]any{
		"type":    "http",
		"url":     a.mcp.srv.URL(),
		"headers": map[string]string{"Authorization": "Bearer " + a.mcp.token},
	}}}, "", "  ")
	return string(b)
}

// mcpSessionList is a copy of the agents' connections, oldest first.
func (a *App) mcpSessionList() []mcpSession {
	a.mcp.mu.Lock()
	defer a.mcp.mu.Unlock()
	out := make([]mcpSession, 0, len(a.mcp.sessions))
	for _, s := range a.mcp.sessions {
		out = append(out, *s)
	}
	return out
}

// dropMCPSession closes one agent connection; false when there is none.
func (a *App) dropMCPSession(id string) bool {
	a.mcp.mu.Lock()
	i := slices.IndexFunc(a.mcp.sessions, func(s *mcpSession) bool { return s.id == id })
	var s *mcpSession
	if i >= 0 {
		s = a.mcp.sessions[i]
		a.mcp.sessions = slices.Delete(a.mcp.sessions, i, i+1)
	}
	a.mcp.mu.Unlock()
	if s == nil {
		return false
	}
	s.release()
	a.invalidate()
	return true
}

func (a *App) mcpSession(id string) (*mcpSession, error) {
	a.mcp.mu.Lock()
	defer a.mcp.mu.Unlock()
	for _, s := range a.mcp.sessions {
		if s.id == id {
			return s, nil
		}
	}
	return nil, fmt.Errorf("no session %q; call connect first", id)
}

const mcpInstructions = "SSHYard holds the user's saved SSH hosts. Call list_groups or list_hosts to find a host, connect with its id to get a session_id, then run_command on that session and disconnect when done. Passwords, keys and passphrases are never revealed; a host that needs one typed is refused. Every command runs on a real server: prefer read-only commands and ask the user before anything destructive."

func schema(props string, required ...string) json.RawMessage {
	req, _ := json.Marshal(required)
	if len(required) == 0 {
		req = []byte("[]")
	}
	return json.RawMessage(`{"type":"object","properties":{` + props + `},"required":` + string(req) + `,"additionalProperties":false}`)
}

// mcpHandler is the endpoint and its tools. The tools run on the server's
// goroutines: they read the store, the keychain and the pool, all safe
// for that, and never the UI's state.
func (a *App) mcpHandler(token string) *mcp.Handler {
	if a.audit == nil && a.p.DataDir != "" {
		a.audit = audit.New(a.p.DataDir)
	}
	aud := a.audit
	version := a.p.Version
	if version == "" {
		version = "dev"
	}
	return &mcp.Handler{
		Name: "sshyard", Version: version, Instructions: mcpInstructions, Token: token,
		Tools: []mcp.Tool{
			{
				Name:        "list_groups",
				Description: "List the host groups and how many hosts each has. Hosts outside any group are counted as ungrouped.",
				Schema:      schema(""),
				Call:        func(context.Context, json.RawMessage) (string, error) { return a.mcpGroups(), nil },
			},
			{
				Name:        "list_hosts",
				Description: "List saved SSH hosts: id, name, group, address and port. Give group to list one group only (an empty string lists the ungrouped hosts).",
				Schema:      schema(`"group":{"type":"string","description":"Only hosts in this group"}`),
				Call:        a.mcpHosts,
			},
			{
				Name:        "connect",
				Description: "Open an SSH connection to a saved host with the credentials saved in SSHYard, which are never revealed. Returns a session_id for run_command. Fails if the host needs a password or passphrase typed, or its host key is not trusted yet.",
				Schema:      schema(`"host_id":{"type":"string","description":"The id from list_hosts"}`, "host_id"),
				Call:        a.mcpConnect,
			},
			{
				Name:        "run_command",
				Description: "Run a shell command (sh -c) on a connected host and return its exit code and combined output (at most 1 MiB). The command is stopped when it times out or the request is cancelled.",
				Schema: schema(`"session_id":{"type":"string"},`+
					`"command":{"type":"string","description":"The shell command"},`+
					`"cwd":{"type":"string","description":"Remote working directory; empty keeps the login directory"},`+
					`"timeout_seconds":{"type":"integer","minimum":5,"maximum":1800,"description":"Default 60"}`, "session_id", "command"),
				Call: func(ctx context.Context, raw json.RawMessage) (string, error) { return a.mcpRun(ctx, raw, aud) },
			},
			{
				Name:        "list_sessions",
				Description: "List the open SSH sessions made with connect.",
				Schema:      schema(""),
				Call:        func(context.Context, json.RawMessage) (string, error) { return a.mcpSessions(), nil },
			},
			{
				Name:        "disconnect",
				Description: "Close a session made with connect.",
				Schema:      schema(`"session_id":{"type":"string"}`, "session_id"),
				Call:        a.mcpDisconnect,
			},
		},
	}
}

func toJSON(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

type mcpHostInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Group   string `json:"group"`
	Address string `json:"address"`
	Port    int    `json:"port"`
}

func mcpHostOf(h assets.Host) mcpHostInfo {
	port := h.Port
	if port == 0 {
		port = 22
	}
	return mcpHostInfo{ID: h.ID, Name: h.Label(), Group: h.Group, Address: h.Address, Port: port}
}

func (a *App) mcpGroups() string {
	type group struct {
		Name  string `json:"name"`
		Hosts int    `json:"hosts"`
	}
	count := map[string]int{}
	for _, h := range a.store.List() {
		count[h.Group]++
	}
	groups := []group{}
	for _, g := range a.store.Groups() {
		groups = append(groups, group{g, count[g]})
	}
	return toJSON(map[string]any{"groups": groups, "ungrouped_hosts": count[""]})
}

func (a *App) mcpHosts(_ context.Context, raw json.RawMessage) (string, error) {
	var args struct {
		Group *string `json:"group"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", err
	}
	hosts := []mcpHostInfo{}
	for _, h := range a.store.List() {
		if args.Group == nil || h.Group == *args.Group {
			hosts = append(hosts, mcpHostOf(h))
		}
	}
	return toJSON(map[string]any{"hosts": hosts}), nil
}

// mcpLogin is how h logs in for an agent: only what is saved, never what
// was typed this run. Every hop of h's jump chain is checked up front, as
// the pool may already hold a connection the user typed a password for.
func (a *App) mcpLogin(h assets.Host) error {
	path, err := a.store.JumpPath(h.ID)
	if err != nil {
		return err
	}
	for _, hop := range append(path, h) {
		switch hop.Auth {
		case assets.AuthAgent:
		case assets.AuthCredential:
			cr, ok := a.store.Credential(hop.CredentialID)
			if !ok || cr.AskPassphrase {
				return errMCPNeedsInput
			}
		default:
			return errMCPNeedsInput
		}
	}
	return nil
}

// mcpCredentials reads h's saved secrets from the keychain for the dial;
// they go to the SSH handshake and nowhere else.
func (a *App) mcpCredentials(h assets.Host) (session.Credentials, error) {
	var out session.Credentials
	if h.Auth == assets.AuthAgent {
		out.Agent = true
		return out, nil
	}
	cr, ok := a.store.Credential(h.CredentialID)
	if h.Auth != assets.AuthCredential || !ok || cr.AskPassphrase {
		return out, errMCPNeedsInput
	}
	out.User = cr.User
	switch cr.Kind {
	case assets.CredPassword:
		b, err := a.kc.Get(keychain.Cred(cr.ID, keychain.Password))
		if err != nil {
			return out, errMCPNeedsInput
		}
		out.Password = string(b)
	case assets.CredKey:
		key, err := a.kc.Get(keychain.Cred(cr.ID, keychain.PrivateKey))
		if err != nil {
			return out, errMCPNeedsInput
		}
		out.PrivateKey = key
		out.Passphrase, err = a.kc.Get(keychain.Cred(cr.ID, keychain.Passphrase))
		if err != nil && cr.Encrypted {
			return out, errMCPNeedsInput
		}
	}
	return out, nil
}

func (a *App) mcpConnect(ctx context.Context, raw json.RawMessage) (string, error) {
	var args struct {
		HostID string `json:"host_id"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", err
	}
	h, ok := a.store.Get(args.HostID)
	if !ok {
		return "", fmt.Errorf("no host %q; see list_hosts", args.HostID)
	}
	if err := a.mcpLogin(h); err != nil {
		return "", err
	}
	a.mcp.mu.Lock()
	full := len(a.mcp.sessions) >= mcpMaxSessions
	a.mcp.mu.Unlock()
	if full {
		return "", fmt.Errorf("%d sessions are open; disconnect one first", mcpMaxSessions)
	}
	cfg := a.hostConfig(h, nil, func(context.Context) (session.Credentials, error) { return a.mcpCredentials(h) })
	for i := range cfg.Jumps {
		id := cfg.Jumps[i].ID
		cfg.Jumps[i].Credentials = func(context.Context) (session.Credentials, error) {
			j, ok := a.store.Get(id)
			if !ok {
				return session.Credentials{}, errMCPNeedsInput
			}
			return a.mcpCredentials(j)
		}
	}
	client, release, err := a.pool.Acquire(ctx, a.poolKey(h), cfg)
	if err != nil {
		return "", mcpDialErr(err)
	}
	s := &mcpSession{host: h, client: client, release: release, since: time.Now()}
	a.mcp.mu.Lock()
	a.mcp.next++
	s.id = "s" + strconv.Itoa(a.mcp.next)
	a.mcp.sessions = append(a.mcp.sessions, s)
	a.mcp.mu.Unlock()
	a.invalidate()
	return toJSON(map[string]any{"session_id": s.id, "host": mcpHostOf(h)}), nil
}

// mcpDialErr says why a connection failed, in words for the agent.
func mcpDialErr(err error) error {
	var unknown *session.UnknownHostError
	var changed *session.HostKeyChangedError
	switch {
	case errors.As(err, &unknown):
		return fmt.Errorf("the host key of %s is not trusted yet; connect to it once in SSHYard and trust the key", unknown.Host)
	case errors.As(err, &changed):
		return fmt.Errorf("the host key of %s changed; SSHYard refused to connect", changed.Host)
	case errors.Is(err, errMCPNeedsInput), errors.Is(err, session.ErrNoCredentials),
		errors.Is(err, session.ErrPassphraseNeeded), errors.Is(err, session.ErrPassphraseWrong):
		return errMCPNeedsInput
	}
	return fmt.Errorf("connect failed: %v", err)
}

func (a *App) mcpRun(ctx context.Context, raw json.RawMessage, aud *audit.Log) (string, error) {
	var args struct {
		SessionID string `json:"session_id"`
		Command   string `json:"command"`
		Cwd       string `json:"cwd"`
		Timeout   int    `json:"timeout_seconds"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Command) == "" {
		return "", errors.New("empty command")
	}
	s, err := a.mcpSession(args.SessionID)
	if err != nil {
		return "", err
	}
	timeout := mcpTimeout
	if args.Timeout > 0 {
		timeout = min(max(time.Duration(args.Timeout)*time.Second, remote.MinCommandTimeout), remote.MaxCommandTimeout)
	}
	run := remote.New(s.client, remote.Options{CommandTimeout: timeout, TurnTimeout: remote.MaxCommandTimeout})
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	out := &commandOutput{remaining: (1 << 20) - 4096, cancel: cancel}
	counted := &remote.CountingWriter{W: out}
	start := time.Now()
	code, err := run.Run(runCtx, args.Command, args.Cwd, counted, counted)
	text, limited := out.result()
	if aud != nil {
		grade := policy.NewEngine().Classify(policy.Action{Where: policy.Remote, Tool: "run_command", Command: args.Command, Dir: args.Cwd},
			policy.Scope{WritableRoots: s.host.AIPolicy.WritableRoots, Sensitive: s.host.AIPolicy.Sensitive})
		exit := code
		_ = aud.Record(policy.AuditRecord{
			Time: start, SessionID: "mcp-" + s.id, PaneID: "mcp", Agent: "mcp", Host: s.host.ID, Mode: "mcp",
			Where: string(policy.Remote), Category: string(grade.Category), Risk: grade.Risk.String(),
			Command: policy.Redact(args.Command), Verdict: "mcp", Decider: "mcp", ExitCode: &exit,
			DurationMS: time.Since(start).Milliseconds(), OutputBytes: counted.Bytes(),
		})
	}
	switch {
	case limited && ctx.Err() == nil:
		return "", errors.New(joinOut("output passed 1 MiB; the command was stopped", text))
	case errors.Is(err, context.DeadlineExceeded):
		return "", errors.New(joinOut(fmt.Sprintf("timed out after %s; the command was stopped", timeout), text))
	case err != nil:
		return "", errors.New(joinOut(err.Error(), text))
	}
	return joinOut("exit_code: "+strconv.Itoa(code), text), nil
}

func (a *App) mcpSessions() string {
	type info struct {
		SessionID string      `json:"session_id"`
		Host      mcpHostInfo `json:"host"`
		Since     time.Time   `json:"since"`
	}
	list := []info{}
	for _, s := range a.mcpSessionList() {
		list = append(list, info{s.id, mcpHostOf(s.host), s.since.UTC().Truncate(time.Second)})
	}
	return toJSON(map[string]any{"sessions": list})
}

func (a *App) mcpDisconnect(_ context.Context, raw json.RawMessage) (string, error) {
	var args struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return "", err
	}
	if !a.dropMCPSession(args.SessionID) {
		return "", fmt.Errorf("no session %q", args.SessionID)
	}
	return "disconnected " + args.SessionID, nil
}
