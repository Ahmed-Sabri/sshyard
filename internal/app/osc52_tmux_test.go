package app

import (
	"bytes"
	"encoding/base64"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/sshtest"
	"github.com/creack/pty"
	"golang.org/x/crypto/ssh"
)

// byteLog is what tmux wrote to the SSH channel.
type byteLog struct {
	mu sync.Mutex
	b  []byte
}

func (l *byteLog) add(p []byte) {
	l.mu.Lock()
	l.b = append(l.b, append([]byte(nil), p...)...)
	l.mu.Unlock()
}

func (l *byteLog) take() []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.b
	l.b = nil
	return b
}

// osc52Snippets returns the OSC 52 sequences in raw, each up to its BEL or ST.
func osc52Snippets(raw []byte) []string {
	var out []string
	for from := 0; from < len(raw); {
		i := bytes.Index(raw[from:], []byte("\x1b]52;"))
		if i < 0 {
			break
		}
		i += from
		rest := raw[i:]
		end := bytes.IndexByte(rest, 0x07)
		st := bytes.Index(rest, []byte("\x1b\\"))
		var n int
		switch {
		case end >= 0 && (st < 0 || end < st):
			n = end + 1
		case st >= 0:
			n = st + 2
		default:
			n = min(len(rest), 80)
		}
		out = append(out, string(rest[:n]))
		from = i + 4
	}
	return out
}

// tmuxPane is the first pane id, or "" while the server has no session.
// The socket file exists before new-session has a target; send-keys then
// exits 1 with "no current target".
func tmuxPane(sock string) string {
	cmd := exec.Command("tmux", "-S", sock, "list-panes", "-F", "#{pane_id}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(string(out), "\n")
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "%") {
		return ""
	}
	return line
}

func tmuxStarting(out []byte) bool {
	s := string(out)
	return strings.Contains(s, "no current target") ||
		strings.Contains(s, "no server") ||
		strings.Contains(s, "no sessions") ||
		strings.Contains(s, "can't find") ||
		strings.Contains(s, "error connecting") ||
		strings.Contains(s, "lost server")
}

// tmuxCmd runs args against the live pane. It polls until list-panes
// returns a pane and the command succeeds. A missing target is retried;
// any other tmux error fails at once.
func tmuxCmd(t *testing.T, tt *ui.Tester, sock string, args ...string) {
	t.Helper()
	if len(args) == 0 {
		t.Fatal("tmux command")
	}
	deadline := time.Now().Add(5 * time.Second)
	var out []byte
	var err error
	for {
		tt.Frame()
		if pane := tmuxPane(sock); pane != "" {
			cmdArgs := append([]string{"-S", sock, args[0], "-t", pane}, args[1:]...)
			out, err = exec.Command("tmux", cmdArgs...).CombinedOutput()
			if err == nil {
				return
			}
			if !tmuxStarting(out) {
				t.Fatalf("tmux %v: %v\n%s", args, err, out)
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("tmux %v: %v\n%s", args, err, out)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// tmuxPTY runs an attached tmux as the SSH shell and records its output.
func tmuxPTY(cfg, sock string, log *byteLog) sshtest.Shell {
	return func(ch ssh.Channel, p sshtest.PTY, resize <-chan sshtest.PTY) int {
		cmd := exec.Command("tmux", "-f", cfg, "-S", sock, "new-session", "-x", strconv.Itoa(max(p.Cols, 1)), "-y", strconv.Itoa(max(p.Rows, 1)))
		term := p.Term
		if term == "" {
			term = "xterm-256color"
		}
		cmd.Env = append(os.Environ(), "TERM="+term)
		f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(max(p.Cols, 1)), Rows: uint16(max(p.Rows, 1))})
		if err != nil {
			return 1
		}
		// Setsize and the stdin copy both use f. They must finish before
		// Close, or Fd races with it and the goroutines outlive the test.
		done := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				case s, ok := <-resize:
					if !ok {
						return
					}
					pty.Setsize(f, &pty.Winsize{Cols: uint16(max(s.Cols, 1)), Rows: uint16(max(s.Rows, 1))})
				}
			}
		}()
		go func() {
			defer wg.Done()
			io.Copy(f, ch)
		}()
		buf := make([]byte, 32<<10)
		for {
			n, err := f.Read(buf)
			if n > 0 {
				log.add(buf[:n])
				ch.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
		close(done)
		ch.Close() // unblocks the stdin copy
		wg.Wait()
		f.Close()
		if err := cmd.Wait(); err != nil {
			if e, ok := err.(*exec.ExitError); ok {
				return e.ExitCode()
			}
			return 1
		}
		return 0
	}
}

// TestOSC52RealTmux runs acceptance criteria 3 and 4 against tmux 3.x
// over the in-process SSH server. It skips on Windows, when a pty cannot
// be opened, and when tmux is not installed.
//
// What arrived, with tmux 3.5a as the outer program (TERM=xterm-256color,
// which has the Ms capability): tmux does not forward the DCS wrapper.
// copy-mode's copy-selection writes a plain OSC 52 with an empty
// selection and BEL, "\x1b]52;;<base64>\a". allow-passthrough unwraps
// "\x1bPtmux;\x1b\x1b]52;c;...\a\x1b\\" and the SSH channel receives the
// inner OSC 52, "\x1b]52;c;<base64>\a".
func TestOSC52RealTmux(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("not run on windows")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not on PATH")
	}
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skip("pty unsupported")
	}
	ptmx.Close()
	tty.Close()
	dir := t.TempDir()
	if len(filepath.Join(dir, "sock")) > 100 {
		var err error
		dir, err = os.MkdirTemp("/tmp", "osc52")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(dir) })
	}
	cfg := filepath.Join(dir, "tmux.conf")
	sock := filepath.Join(dir, "sock")
	conf := "set -g set-clipboard on\nset -g allow-passthrough on\nset -g mode-keys vi\nset -g status off\nset -sg escape-time 0\n"
	if err := os.WriteFile(cfg, []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	log := &byteLog{}
	shellDone := make(chan struct{})
	var shellStarted atomic.Bool
	// Registered before the app and the server, so it runs after they
	// close: kill tmux, then wait until the shell has joined its goroutines.
	t.Cleanup(func() {
		exec.Command("tmux", "-S", sock, "kill-server").Run()
		if shellStarted.Load() {
			<-shellDone
		}
	})

	srv := startServer(t)
	srv.Shell = func(ch ssh.Channel, p sshtest.PTY, resize <-chan sshtest.PTY) int {
		shellStarted.Store(true)
		defer close(shellDone)
		return tmuxPTY(cfg, sock, log)(ch, p, resize)
	}

	a, tt := newTestApp(t)
	h := addHost(t, a, srv, "box", "secret")
	a.known.Add(srv.Addr, srv.HostKey.PublicKey())
	if _, err := a.Connect(h.ID, openAt{}); err != nil {
		t.Fatal(err)
	}
	c := a.focused()
	waitFor(t, tt, "connected", func() bool { return c.s.Status().State == session.Connected })
	waitFor(t, tt, "tmux socket", func() bool {
		_, err := os.Stat(sock)
		return err == nil
	})

	// Criterion 3: copy-mode select + copy, with set-clipboard on.
	// The marker is only in the script's output, so search cannot land
	// on the typed command.
	clipScript := filepath.Join(dir, "clip.sh")
	if err := os.WriteFile(clipScript, []byte("#!/bin/sh\nprintf 'CLIPLINE\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	tmuxCmd(t, tt, sock, "send-keys", "sh "+clipScript, "Enter")
	waitFor(t, tt, "CLIPLINE on screen", func() bool {
		return strings.Contains(c.s.Term.ScreenText(), "CLIPLINE")
	})
	log.take()
	tmuxCmd(t, tt, sock, "copy-mode")
	tmuxCmd(t, tt, sock, "send-keys", "-X", "search-backward", "CLIPLINE")
	tmuxCmd(t, tt, sock, "send-keys", "-X", "select-line")
	tmuxCmd(t, tt, sock, "send-keys", "-X", "copy-selection-and-cancel")
	line := "CLIPLINE\n"
	waitFor(t, tt, "copy-mode clipboard", func() bool { return tt.Clipboard() == line })
	raw := log.take()
	want := "\x1b]52;;" + base64.StdEncoding.EncodeToString([]byte(line)) + "\a"
	if !bytes.Contains(raw, []byte(want)) {
		t.Fatalf("copy-mode arrived as %q, want %q", osc52Snippets(raw), want)
	}
	if !tt.HasText("box wrote to the clipboard (9 bytes)") {
		t.Fatalf("toast: %q", tt.Texts())
	}
	t.Logf("copy-mode OSC 52: %q", osc52Snippets(raw))

	// Criterion 4: passthrough inside tmux. tmux unwraps it.
	b64 := base64.StdEncoding.EncodeToString([]byte("hello"))
	script := filepath.Join(dir, "pass.sh")
	body := "#!/bin/sh\nprintf '\\033Ptmux;\\033\\033]52;c;" + b64 + "\\007\\033\\\\'\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	tmuxCmd(t, tt, sock, "send-keys", "sh "+script, "Enter")
	waitFor(t, tt, "passthrough clipboard", func() bool { return tt.Clipboard() == "hello" })
	raw = log.take()
	want = "\x1b]52;c;" + b64 + "\a"
	if !bytes.Contains(raw, []byte(want)) {
		t.Fatalf("passthrough arrived as %q, want %q", osc52Snippets(raw), want)
	}
	if bytes.Contains(raw, []byte("\x1bPtmux")) {
		t.Fatalf("tmux forwarded the DCS wrapper: %q", osc52Snippets(raw))
	}
	t.Logf("passthrough OSC 52: %q", osc52Snippets(raw))
}
