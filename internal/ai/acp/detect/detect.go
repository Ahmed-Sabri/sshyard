// Package detect finds ACP agents installed on this computer, so the user
// picks one instead of typing a command line.
//
// An agent is found in three steps: its executable on the search path (the
// login shell's PATH and the usual per-user install directories, which an
// app started from the desktop does not inherit), then the way it speaks
// ACP (a separate adapter, or its own flag or subcommand, confirmed in its
// help text), then its version, best effort.
//
// Detection only runs the agents' --help and --version; nothing is
// installed. An agent that needs an adapter it does not have runs it
// through npx when npx is present, and otherwise reports the npm package
// to install.
package detect

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Licoy/sshyard/internal/ai/acp"
)

// Agent is one ACP agent found on this computer.
type Agent struct {
	ID      string // catalog id: claude, codex, grok, gemini, ...
	Name    string
	Type    string // acp.Config.Type
	Command string // absolute path; empty when Missing is set
	Args    []string
	Version string // the agent's own version, when it reports one
	// Fetch is set when Command is npx, which downloads Package the first
	// time the agent starts.
	Fetch bool
	// Package is the npm package of the agent's ACP adapter, when it needs one.
	Package string
	// Missing is set when the agent is installed but its adapter is not,
	// and there is no npx to fetch it: install Package first.
	Missing bool
}

// variant is one way an agent speaks ACP by itself: help prints a text
// that must name every entry of want (a flag, or a subcommand when it does
// not start with '-'), then the agent runs with args.
type variant struct {
	help []string
	want []string
	args []string
}

// entry is one agent the catalog knows.
type entry struct {
	id, name, typ string
	cli           string    // the agent's own command, for its version
	adapters      []string  // separate ACP adapters, preferred over the CLI
	pkg           string    // npm package of the adapter
	native        []variant // the CLI's own ACP mode, the first one its help names
	env           []string  // its own variables; a name ending in '_' is a prefix
}

var help = []string{"--help"}

// catalog is in the order the agents are listed.
var catalog = []entry{
	{id: "claude", name: "Claude Code", typ: "claude", cli: "claude",
		adapters: []string{"claude-agent-acp", "claude-code-acp"}, pkg: "@agentclientprotocol/claude-agent-acp",
		env: []string{"ANTHROPIC_", "CLAUDE_"}},
	{id: "codex", name: "Codex", typ: "codex", cli: "codex",
		adapters: []string{"codex-acp"}, pkg: "@agentclientprotocol/codex-acp",
		env: []string{"OPENAI_", "CODEX_"}},
	{id: "grok", name: "Grok", typ: "grok", cli: "grok",
		native: []variant{{help: []string{"agent", "--help"}, want: []string{"stdio"}, args: []string{"agent", "stdio"}}},
		env:    []string{"XAI_", "GROK_"}},
	{id: "gemini", name: "Gemini CLI", typ: "custom", cli: "gemini", native: acpFlag,
		env: []string{"GEMINI_", "GOOGLE_API_KEY", "GOOGLE_GENAI_", "GOOGLE_CLOUD_PROJECT", "GOOGLE_CLOUD_LOCATION"}},
	{id: "qwen", name: "Qwen Code", typ: "custom", cli: "qwen", native: acpFlag,
		env: []string{"QWEN_", "DASHSCOPE_", "OPENAI_"}},
	{id: "copilot", name: "GitHub Copilot CLI", typ: "custom", cli: "copilot", native: []variant{
		{help: help, want: []string{"--acp", "--stdio"}, args: []string{"--acp", "--stdio"}},
		{help: help, want: []string{"--acp"}, args: []string{"--acp"}},
	}, env: []string{"COPILOT_", "GH_TOKEN", "GITHUB_TOKEN"}},
	{id: "kimi", name: "Kimi CLI", typ: "custom", cli: "kimi", native: acpCommand, env: []string{"KIMI_", "MOONSHOT_"}},
	{id: "opencode", name: "OpenCode", typ: "custom", cli: "opencode", native: acpCommand, env: []string{"OPENCODE_"}},
	{id: "goose", name: "Goose", typ: "custom", cli: "goose", native: acpCommand, env: []string{"GOOSE_"}},
	{id: "auggie", name: "Auggie", typ: "custom", cli: "auggie", native: acpFlag, env: []string{"AUGMENT_"}},
	{id: "codebuddy", name: "CodeBuddy Code", typ: "custom", cli: "codebuddy", native: acpFlag, env: []string{"CODEBUDDY_"}},
	{id: "vibe", name: "Mistral Vibe", typ: "custom", cli: "vibe", adapters: []string{"vibe-acp"}, env: []string{"MISTRAL_", "VIBE_"}},
}

// acpFlag prefers the stable flag over the experimental one.
var acpFlag = []variant{
	{help: help, want: []string{"--acp"}, args: []string{"--acp"}},
	{help: help, want: []string{"--experimental-acp"}, args: []string{"--experimental-acp"}},
}

// acpCommand is an acp subcommand, or an older --acp flag.
var acpCommand = []variant{
	{help: help, want: []string{"acp"}, args: []string{"acp"}},
	{help: help, want: []string{"--acp"}, args: []string{"--acp"}},
}

// Name is the display name of a catalog id, or "" for an unknown one.
func Name(id string) string {
	for _, e := range catalog {
		if e.id == id {
			return e.name
		}
	}
	return ""
}

// Type is the acp.Config.Type of a catalog id; an unknown one is custom.
func Type(id string) string {
	for _, e := range catalog {
		if e.id == id {
			return e.typ
		}
	}
	return "custom"
}

// shared are passed to every agent: the network proxy and the certificate
// authorities its requests need.
// The proxy variables come first.
var shared = []string{
	"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY",
	"http_proxy", "https_proxy", "all_proxy", "no_proxy",
	"NODE_EXTRA_CA_CERTS", "SSL_CERT_FILE", "SSL_CERT_DIR",
}

// Vars are the variables an agent runs with beyond the base environment
// acp.FilterEnv keeps: the search path, the shared variables and the
// agent's own, from the login shell, else from environ (os.Environ), and
// the system proxy when no proxy variable is set. An
// unknown id, a command the user typed, gets the shared ones.
// acp.FilterEnv still removes the names it always denies.
func Vars(id string, sh Shell, environ []string) map[string]string {
	want := shared
	for _, e := range catalog {
		if e.id == id {
			want = append(slices.Clone(shared), e.env...)
		}
	}
	match := func(name string) bool {
		for _, w := range want {
			if name == w || strings.HasSuffix(w, "_") && strings.HasPrefix(name, w) {
				return true
			}
		}
		return false
	}
	out := map[string]string{}
	for _, kv := range environ {
		if name, value, ok := strings.Cut(kv, "="); ok && match(name) {
			out[name] = value
		}
	}
	for name, value := range sh.Env {
		if match(name) {
			out[name] = value
		}
	}
	if !slices.ContainsFunc(shared[:8], func(name string) bool { return out[name] != "" }) {
		for name, value := range sh.Proxy {
			out[name] = value
		}
	}
	if sh.Path != "" {
		out["PATH"] = sh.Path
	}
	return out
}

// probeTimeout bounds one --help or --version.
const probeTimeout = 8 * time.Second

// Scan lists the agents found, in catalog order. refresh reads the login
// shell again. It blocks for seconds at most; call it off the UI thread.
func Scan(ctx context.Context, refresh bool) []Agent {
	return newScanner(LoginShell(ctx, refresh).Path).scan(ctx, catalog)
}

// Find detects one agent again, for a saved choice whose executable moved.
func Find(ctx context.Context, id string) (Agent, bool) {
	for _, e := range catalog {
		if e.id == id {
			found := newScanner(LoginShell(ctx, false).Path).scan(ctx, []entry{e})
			if len(found) == 1 && !found[0].Missing {
				return found[0], true
			}
		}
	}
	return Agent{}, false
}

// scanner holds what one scan learns, so an executable's help runs once.
type scanner struct {
	path string
	goos string
	run  func(ctx context.Context, bin string, args []string) string

	mu   sync.Mutex
	seen map[string]string
}

func newScanner(path string) *scanner {
	s := &scanner{path: path, goos: runtime.GOOS, seen: map[string]string{}}
	s.run = s.exec
	return s
}

func (s *scanner) scan(ctx context.Context, entries []entry) []Agent {
	found := make([]*Agent, len(entries))
	var wg sync.WaitGroup
	for i, e := range entries {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if a, ok := s.detect(ctx, e); ok {
				found[i] = &a
			}
		}()
	}
	wg.Wait()
	var out []Agent
	for _, a := range found {
		if a != nil {
			out = append(out, *a)
		}
	}
	return out
}

func (s *scanner) detect(ctx context.Context, e entry) (Agent, bool) {
	a := Agent{ID: e.id, Name: e.name, Type: e.typ, Package: e.pkg}
	cli := ""
	if e.cli != "" {
		cli = lookPath(s.path, e.cli, s.goos)
	}
	if cli != "" {
		a.Version = version(s.output(ctx, cli, []string{"--version"}))
	}
	for _, name := range e.adapters {
		if bin := lookPath(s.path, name, s.goos); bin != "" {
			a.Command = bin
			return a, true
		}
	}
	if cli == "" {
		return Agent{}, false
	}
	if e.pkg != "" {
		if npx := lookPath(s.path, "npx", s.goos); npx != "" {
			a.Command, a.Args, a.Fetch = npx, []string{"-y", e.pkg}, true
		} else {
			a.Missing = true
		}
		return a, true
	}
	for _, v := range e.native {
		if names(s.output(ctx, cli, v.help), v.want) {
			a.Command, a.Args = cli, v.args
			return a, true
		}
	}
	return Agent{}, false
}

// output runs bin once per argument list and scan.
func (s *scanner) output(ctx context.Context, bin string, args []string) string {
	key := bin + "\x00" + strings.Join(args, "\x00")
	s.mu.Lock()
	out, ok := s.seen[key]
	s.mu.Unlock()
	if ok {
		return out
	}
	out = s.run(ctx, bin, args)
	s.mu.Lock()
	s.seen[key] = out
	s.mu.Unlock()
	return out
}

// outputCap bounds the help text kept from one probe.
const outputCap = 256 << 10

type capped struct{ bytes.Buffer }

func (c *capped) Write(p []byte) (int, error) {
	if room := outputCap - c.Len(); room > 0 {
		c.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

// exec runs a probe with no input, the agent's environment and a deadline.
func (s *scanner) exec(ctx context.Context, bin string, args []string) string {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = acp.FilterEnv(os.Environ(), map[string]string{"PATH": s.path, "NO_COLOR": "1"})
	cmd.Dir = os.TempDir()
	cmd.WaitDelay = time.Second
	var out capped
	cmd.Stdout, cmd.Stderr = &out, &out
	_ = cmd.Run()
	return out.String()
}

var (
	ansi    = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	semver  = regexp.MustCompile(`\bv?(\d+(?:\.\d+){1,3}(?:[-+][0-9A-Za-z.-]+)?)\b`)
	failure = regexp.MustCompile(`\b(?:Error|TypeError|ReferenceError|SyntaxError|ERR_[A-Z_]+)\b|^\s*at\s`)
)

// version is the version number --version printed on its first line, or
// "" when the line is an error or has none.
func version(out string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(ansi.ReplaceAllString(out, "")), "\n")
	if failure.MatchString(line) {
		return ""
	}
	if m := semver.FindStringSubmatch(line); m != nil {
		return m[1]
	}
	return ""
}

// names reports whether help text lists every flag or subcommand in want.
func names(out string, want []string) bool {
	out = ansi.ReplaceAllString(out, "")
	for _, w := range want {
		var re *regexp.Regexp
		if strings.HasPrefix(w, "-") {
			// A whole flag: --acp does not match --experimental-acp or --acp-port.
			re = regexp.MustCompile(`(?:^|[\s,\[(|])` + regexp.QuoteMeta(w) + `(?:[\s,=\])|]|$)`)
		} else {
			// A subcommand starts an indented line of a command list.
			re = regexp.MustCompile(`(?m)^[ \t]+` + regexp.QuoteMeta(w) + `(?:[ \t,]|$)`)
		}
		if !re.MatchString(out) {
			return false
		}
	}
	return true
}

// LookPath finds an executable named name in a PATH list, as the system
// would; "" when there is none.
func LookPath(path, name string) string { return lookPath(path, name, runtime.GOOS) }

// lookPath finds an executable named name in a PATH list; on Windows it
// tries the executable extensions.
func lookPath(path, name, goos string) string {
	exts := []string{""}
	if goos == "windows" {
		exts = []string{".exe", ".cmd", ".bat", ".com"}
		if filepath.Ext(name) != "" {
			exts = append([]string{""}, exts...)
		}
	}
	for _, dir := range filepath.SplitList(path) {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		for _, ext := range exts {
			p := filepath.Join(dir, name+ext)
			st, err := os.Stat(p)
			if err != nil || st.IsDir() {
				continue
			}
			if goos == "windows" || st.Mode().Perm()&0o111 != 0 {
				return p
			}
		}
	}
	return ""
}
