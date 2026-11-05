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
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	shellMu     sync.Mutex
	shellCached *Shell
)

// Shell is what an agent needs from the user's environment that an app
// started from the desktop may not have: the login shell's variables, and
// the search path.
type Shell struct {
	// Path is this process's PATH, then the login shell's, then the usual
	// per-user install directories that exist.
	Path string
	// Env is the login shell's environment, or nil when it could not be read.
	Env map[string]string
	// Proxy is the system proxy setting as proxy variables, used when no
	// proxy variable is set.
	Proxy map[string]string
}

// LoginShell reads the login shell once; refresh reads it again. It blocks
// for seconds at most; call it off the UI thread.
func LoginShell(ctx context.Context, refresh bool) Shell {
	shellMu.Lock()
	defer shellMu.Unlock()
	if shellCached == nil || refresh {
		home, _ := os.UserHomeDir()
		env := loginEnv(ctx)
		shellCached = &Shell{Path: mergePath(os.Getenv("PATH"), env["PATH"], knownDirs(runtime.GOOS, home, os.Getenv)),
			Env: env, Proxy: systemProxy(ctx, runtime.GOOS)}
	}
	return *shellCached
}

// mergePath joins PATH lists in order, keeping the first of each absolute
// directory.
func mergePath(lists ...string) string {
	var out []string
	seen := map[string]bool{}
	for _, list := range lists {
		for _, dir := range filepath.SplitList(list) {
			dir = filepath.Clean(dir)
			if dir == "" || !filepath.IsAbs(dir) || seen[dir] {
				continue
			}
			seen[dir] = true
			out = append(out, dir)
		}
	}
	return strings.Join(out, string(filepath.ListSeparator))
}

const loginTimeout = 5 * time.Second

// marker brackets what the login shell prints, apart from anything its
// startup files print.
const marker = "\x1fSSHYARD-ENV\x1f"

// loginEnv is the environment of the user's interactive login shell, where
// version managers, installers and proxies set their variables; nil when
// it fails.
func loginEnv(ctx context.Context) map[string]string {
	if runtime.GOOS == "windows" {
		return nil
	}
	shell := os.Getenv("SHELL")
	if !filepath.IsAbs(shell) {
		shell = "/bin/sh"
		if runtime.GOOS == "darwin" {
			shell = "/bin/zsh"
		}
	}
	ctx, cancel := context.WithTimeout(ctx, loginTimeout)
	defer cancel()
	// printf and env are the same in sh, bash, zsh and fish.
	cmd := exec.CommandContext(ctx, shell, "-ilc", "printf '"+marker+"'; env; printf '"+marker+"'")
	cmd.Dir = os.TempDir()
	cmd.WaitDelay = time.Second
	var out capped
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil && out.Len() == 0 {
		return nil
	}
	return parseEnv(between(out.Bytes(), marker))
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// parseEnv reads env's output; a line that does not start a variable
// continues the value before it.
func parseEnv(text string) map[string]string {
	if text == "" {
		return nil
	}
	out := map[string]string{}
	last := ""
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		if envName.MatchString(line) {
			name, value, _ := strings.Cut(line, "=")
			out[name], last = value, name
		} else if last != "" {
			out[last] += "\n" + line
		}
	}
	return out
}

// between is the text between the last two markers.
func between(b []byte, mark string) string {
	end := bytes.LastIndex(b, []byte(mark))
	if end < 0 {
		return ""
	}
	start := bytes.LastIndex(b[:end], []byte(mark))
	if start < 0 {
		return ""
	}
	return string(b[start+len(mark) : end])
}

// knownDirs are the directories package managers and agent installers put
// executables in, that exist, as a PATH list.
func knownDirs(goos, home string, getenv func(string) string) string {
	var dirs []string
	if goos == "windows" {
		for _, d := range []struct{ env, sub string }{
			{"APPDATA", "npm"}, {"LOCALAPPDATA", "pnpm"}, {"LOCALAPPDATA", `Yarn\bin`},
			{"LOCALAPPDATA", `Programs\nodejs`}, {"ProgramFiles", "nodejs"},
		} {
			if base := getenv(d.env); base != "" {
				dirs = append(dirs, filepath.Join(base, d.sub))
			}
		}
		if home != "" {
			for _, sub := range []string{`.bun\bin`, `.local\bin`, `.volta\bin`, `.cargo\bin`, `scoop\shims`, `.grok\bin`} {
				dirs = append(dirs, filepath.Join(home, sub))
			}
		}
	} else {
		if home != "" {
			for _, sub := range []string{".local/bin", "bin", ".npm-global/bin", ".bun/bin", ".volta/bin",
				".cargo/bin", ".deno/bin", ".yarn/bin", ".local/share/pnpm", "Library/pnpm",
				".grok/bin", ".opencode/bin", ".claude/local"} {
				dirs = append(dirs, filepath.Join(home, sub))
			}
			dirs = append(dirs, nvmDirs(filepath.Join(home, ".nvm", "versions", "node"))...)
		}
		dirs = append(dirs, "/opt/homebrew/bin", "/usr/local/bin", "/home/linuxbrew/.linuxbrew/bin", "/snap/bin")
	}
	var out []string
	for _, d := range dirs {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			out = append(out, d)
		}
	}
	return strings.Join(out, string(filepath.ListSeparator))
}

// nvmDirs are the bin directories of the Node versions nvm installed,
// newest first.
func nvmDirs(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	slices.SortFunc(names, func(a, b string) int { return compareVersions(b, a) })
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = filepath.Join(root, n, "bin")
	}
	return out
}

// compareVersions orders "v20.1.0"-style names by number.
func compareVersions(a, b string) int {
	pa := strings.Split(strings.TrimPrefix(a, "v"), ".")
	pb := strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := 0; i < max(len(pa), len(pb)); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x - y
		}
	}
	return strings.Compare(a, b)
}
