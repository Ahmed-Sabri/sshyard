package detect

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeBin makes an executable named name in dir.
func fakeBin(t *testing.T, dir, name string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// fakeScanner scans dirs, answering probes from out by "name args".
func fakeScanner(dirs []string, out map[string]string) (*scanner, *atomic.Int32) {
	s := newScanner(strings.Join(dirs, string(filepath.ListSeparator)))
	runs := new(atomic.Int32)
	s.run = func(_ context.Context, bin string, args []string) string {
		runs.Add(1)
		name := strings.TrimSuffix(filepath.Base(bin), ".exe")
		return out[strings.Join(append([]string{name}, args...), " ")]
	}
	return s, runs
}

func TestScanFindsAgentsTheirWay(t *testing.T) {
	bin, npm := t.TempDir(), t.TempDir()
	adapter := fakeBin(t, npm, "codex-acp")
	fakeBin(t, bin, "codex")
	fakeBin(t, bin, "claude")
	grok := fakeBin(t, bin, "grok")
	gemini := fakeBin(t, bin, "gemini")
	opencode := fakeBin(t, bin, "opencode")
	fakeBin(t, bin, "goose") // its help names no acp command
	npx := fakeBin(t, npm, "npx")
	s, runs := fakeScanner([]string{bin, npm}, map[string]string{
		"claude --version":   "2.1.304 (Claude Code)\n",
		"codex --version":    "\x1b[1mcodex-cli 0.166.0\x1b[0m\n",
		"grok --version":     "grok 1.0.46 (2765805b) [stable]\n",
		"grok agent --help":  "Commands:\n  stdio     Run the agent over stdio\n  serve     Serve\n",
		"gemini --version":   "Error: ERR_MODULE_NOT_FOUND 1.2.3\n",
		"gemini --help":      "Options:\n  --experimental-acp  Start in ACP mode\n  --acp-port <n>\n",
		"opencode --help":    "Commands:\n  opencode acp      start ACP server\n  acp        Start an ACP server\n",
		"goose --help":       "Commands:\n  session   Start a session\n",
		"opencode --version": "v0.9.1\n",
	})
	got := map[string]Agent{}
	for _, a := range s.scan(context.Background(), catalog) {
		got[a.ID] = a
	}
	want := map[string]Agent{
		"claude": {ID: "claude", Name: "Claude Code", Type: "claude", Command: npx, Args: []string{"-y", "@agentclientprotocol/claude-agent-acp"},
			Version: "2.1.304", Fetch: true, Package: "@agentclientprotocol/claude-agent-acp"},
		"codex": {ID: "codex", Name: "Codex", Type: "codex", Command: adapter, Version: "0.166.0", Package: "@agentclientprotocol/codex-acp"},
		"grok":  {ID: "grok", Name: "Grok", Type: "grok", Command: grok, Args: []string{"agent", "stdio"}, Version: "1.0.46"},
		// --acp-port is not --acp; a failing --version has no version.
		"gemini":   {ID: "gemini", Name: "Gemini CLI", Type: "custom", Command: gemini, Args: []string{"--experimental-acp"}},
		"opencode": {ID: "opencode", Name: "OpenCode", Type: "custom", Command: opencode, Args: []string{"acp"}, Version: "0.9.1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scan:\n got %+v\nwant %+v", got, want)
	}
	before := runs.Load()
	s.output(context.Background(), gemini, []string{"--help"})
	if runs.Load() != before {
		t.Fatal("a probe ran twice in one scan")
	}
}

func TestScanReportsMissingAdapterWithoutNpx(t *testing.T) {
	bin := t.TempDir()
	fakeBin(t, bin, "claude")
	s, _ := fakeScanner([]string{bin}, nil)
	got := s.scan(context.Background(), catalog)
	if len(got) != 1 || got[0].ID != "claude" || !got[0].Missing || got[0].Command != "" || got[0].Package == "" {
		t.Fatalf("got %+v", got)
	}
}

func TestLookPathNeedsAnExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows finds executables by extension")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "plain"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "folder"), 0o755); err != nil {
		t.Fatal(err)
	}
	exe := fakeBin(t, dir, "tool")
	path := strings.Join([]string{"relative", dir}, string(filepath.ListSeparator))
	if got := lookPath(path, "plain", "linux"); got != "" {
		t.Fatalf("non-executable found: %q", got)
	}
	if got := lookPath(path, "folder", "linux"); got != "" {
		t.Fatalf("directory found: %q", got)
	}
	if got := lookPath(path, "tool", "linux"); got != exe {
		t.Fatalf("got %q", got)
	}
	cmd := filepath.Join(dir, "agent.cmd")
	if err := os.WriteFile(cmd, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := lookPath(dir, "agent", "windows"); got != cmd {
		t.Fatalf("windows extension: %q", got)
	}
	if got := lookPath(dir, "agent.cmd", "windows"); got != cmd {
		t.Fatalf("windows explicit extension: %q", got)
	}
}

func TestNamesMatchesWholeFlagsAndCommands(t *testing.T) {
	help := "Usage: x [--acp] [--stdio]\nCommands:\n  acp   start\n  stdio-proxy  no\n"
	for _, tc := range []struct {
		want []string
		ok   bool
	}{
		{[]string{"--acp"}, true},
		{[]string{"--acp", "--stdio"}, true},
		{[]string{"acp"}, true},
		{[]string{"stdio"}, false},
		{[]string{"--experimental-acp"}, false},
		{[]string{"--ac"}, false},
	} {
		if got := names(help, tc.want); got != tc.ok {
			t.Errorf("names(%q) = %v", tc.want, got)
		}
	}
	if names("  --experimental-acp  ACP mode", []string{"--acp"}) {
		t.Error("--acp matched inside --experimental-acp")
	}
}

func TestVersion(t *testing.T) {
	for out, want := range map[string]string{
		"2.1.304 (Claude Code)\n":       "2.1.304",
		"codex-cli 0.166.0":             "0.166.0",
		"v1.2.3-beta.1\nmore":           "1.2.3-beta.1",
		"TypeError: x is 1.2":           "",
		"    at Object.<anonymous> 1.2": "",
		"no version here":               "",
	} {
		if got := version(out); got != want {
			t.Errorf("version(%q) = %q, want %q", out, got, want)
		}
	}
}

func TestVarsPassesProxyAndTheAgentsOwn(t *testing.T) {
	sh := Shell{
		Path:  "/opt/bin:/usr/bin",
		Env:   map[string]string{"HTTPS_PROXY": "http://shell:1", "ANTHROPIC_BASE_URL": "https://relay", "OPENAI_API_KEY": "other", "AWS_SECRET_ACCESS_KEY": "x"},
		Proxy: map[string]string{"HTTPS_PROXY": "http://system:2", "ALL_PROXY": "http://system:2"},
	}
	environ := []string{"ANTHROPIC_API_KEY=k", "HTTPS_PROXY=http://process:3", "HOME=/h"}
	want := map[string]string{"PATH": "/opt/bin:/usr/bin", "HTTPS_PROXY": "http://shell:1", "ANTHROPIC_BASE_URL": "https://relay", "ANTHROPIC_API_KEY": "k"}
	if got := Vars("claude", sh, environ); !reflect.DeepEqual(got, want) {
		t.Fatalf("claude: %v", got)
	}
	// A typed command gets no agent variables; with no proxy variable the system proxy applies.
	sh.Env = nil
	want = map[string]string{"PATH": "/opt/bin:/usr/bin", "HTTPS_PROXY": "http://system:2", "ALL_PROXY": "http://system:2"}
	if got := Vars("", sh, []string{"ANTHROPIC_API_KEY=k"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("custom: %v", got)
	}
}

func TestMergePathKeepsFirstAbsolute(t *testing.T) {
	sep := string(filepath.ListSeparator)
	abs := func(p string) string { return filepath.Join(string(filepath.Separator), p) }
	got := mergePath(strings.Join([]string{abs("a"), "rel", abs("b")}, sep), strings.Join([]string{abs("b") + string(filepath.Separator), abs("c")}, sep), "")
	if want := strings.Join([]string{abs("a"), abs("b"), abs("c")}, sep); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestLoginShellOutput(t *testing.T) {
	out := "motd " + marker + "\nPATH=/a:/b\nMULTI=one\ntwo\nlast seen\n" + marker + " goodbye"
	env := parseEnv(between([]byte(out), marker))
	if env["PATH"] != "/a:/b" || env["MULTI"] != "one\ntwo\nlast seen" {
		t.Fatalf("got %q", env)
	}
	if between([]byte("no markers"), marker) != "" || parseEnv("") != nil {
		t.Fatal("output without markers was read")
	}
}

func TestNvmDirsNewestFirst(t *testing.T) {
	root := t.TempDir()
	for _, v := range []string{"v9.11.2", "v20.10.0", "v20.9.1"} {
		if err := os.Mkdir(filepath.Join(root, v), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got := nvmDirs(root)
	want := []string{filepath.Join(root, "v20.10.0", "bin"), filepath.Join(root, "v20.9.1", "bin"), filepath.Join(root, "v9.11.2", "bin")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q", got)
	}
}

func TestSystemProxySettings(t *testing.T) {
	scutil := `<dictionary> {
  ExceptionsList : <array> {
    0 : 127.0.0.1
    1 : *.local
    2 : 47.*
  }
  HTTPEnable : 1
  HTTPPort : 7890
  HTTPProxy : 127.0.0.1
  HTTPSEnable : 1
  HTTPSPort : 7890
  HTTPSProxy : 127.0.0.1
  SOCKSEnable : 1
  SOCKSPort : 7891
  SOCKSProxy : 127.0.0.1
}`
	got := parseScutil(scutil)
	for name, want := range map[string]string{
		"HTTP_PROXY": "http://127.0.0.1:7890", "https_proxy": "http://127.0.0.1:7890",
		"ALL_PROXY": "socks5://127.0.0.1:7891", "NO_PROXY": "127.0.0.1,.local",
	} {
		if got[name] != want {
			t.Errorf("scutil %s = %q, want %q", name, got[name], want)
		}
	}
	if parseScutil("<dictionary> {\n  HTTPEnable : 0\n  HTTPProxy : h\n  HTTPPort : 1\n}") != nil {
		t.Error("disabled proxy used")
	}
	reg := "\r\nHKEY_CURRENT_USER\\Software\\Microsoft\\Windows\\CurrentVersion\\Internet Settings\r\n" +
		"    ProxyEnable    REG_DWORD    0x1\r\n    ProxyServer    REG_SZ    http=10.0.0.1:8080;https=10.0.0.1:8443\r\n" +
		"    ProxyOverride    REG_SZ    *.corp;<local>\r\n"
	got = parseInternetSettings(reg)
	for name, want := range map[string]string{
		"HTTP_PROXY": "http://10.0.0.1:8080", "HTTPS_PROXY": "http://10.0.0.1:8443",
		"ALL_PROXY": "http://10.0.0.1:8443", "NO_PROXY": ".corp,localhost,127.0.0.1",
	} {
		if got[name] != want {
			t.Errorf("registry %s = %q, want %q", name, got[name], want)
		}
	}
	if parseInternetSettings(strings.Replace(reg, "0x1", "0x0", 1)) != nil {
		t.Error("disabled Windows proxy used")
	}
}
