package app

import (
	"errors"
	"io"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/vt"
)

func TestSnippetBytes(t *testing.T) {
	for _, tc := range []struct {
		name, content, want string
		enter, bracketed    bool
		unsafe              bool
	}{
		{name: "insert", content: "echo 中文", want: "echo 中文"},
		{name: "execute", content: "echo hi\r\n\n", enter: true, want: "echo hi\r"},
		{name: "literal escapes", content: `echo 中文\n`, want: `echo 中文\n`},
		{name: "preserve whitespace", content: " echo hi  \r\n", want: " echo hi  "},
		{name: "CRLF and bare CR", content: "甲\r\n乙\r丙\n", bracketed: true, want: "\x1b[200~甲\r乙\r丙\x1b[201~"},
		{name: "enter outside paste", content: "甲\n乙\n", enter: true, bracketed: true, want: "\x1b[200~甲\r乙\x1b[201~\r"},
		{name: "unsafe insert multiline", content: "a\nb", unsafe: true},
		{name: "explicit execute multiline", content: "a\nb", enter: true, want: "a\rb\r"},
		{name: "unsafe insert tab", content: "a\tb", unsafe: true},
		{name: "unsafe execute tab", content: "a\tb", enter: true, unsafe: true},
		{name: "safe tab", content: "a\tb", bracketed: true, want: "\x1b[200~a\tb\x1b[201~"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := snippetBytes(tc.content, tc.enter, vt.Modes{BracketedPaste: tc.bracketed})
			if tc.unsafe {
				if !errors.Is(err, errSnippetUnsafePaste) || len(got) != 0 {
					t.Fatalf("unsafe paste: %q, %v", got, err)
				}
			} else if err != nil || string(got) != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	for _, content := range []string{"echo\x1b[201~", "echo\x03", "echo\x00", "echo\x7f"} {
		if b, err := snippetBytes(content, true, vt.Modes{BracketedPaste: true}); err == nil || len(b) != 0 {
			t.Fatalf("invalid control accepted: %q", b)
		}
	}
}

type snippetFailWriter struct {
	n   int
	err error
}

func (w snippetFailWriter) Write([]byte) (int, error) { return w.n, w.err }

func TestSnippetWriteErrors(t *testing.T) {
	remote := errors.New("private remote detail")
	for _, tc := range []struct {
		writer snippetFailWriter
		want   error
	}{
		{snippetFailWriter{n: 1}, io.ErrShortWrite},
		{snippetFailWriter{err: remote}, remote},
		{snippetFailWriter{err: session.ErrInputBusy}, errSnippetBusy},
	} {
		err := writeSnippet(tc.writer, []byte("body"))
		if !errors.Is(err, tc.want) || tc.want != errSnippetBusy && !errors.Is(err, errSnippetSend) {
			t.Fatalf("write error %v, want %v", err, tc.want)
		}
	}
}

func TestSendSnippetFocusedTerminal(t *testing.T) {
	srv, shell := startScript(t, "\x1b[?2004hready")
	a, tt := newTestApp(t)
	c := connected(t, a, tt, srv)
	waitFor(t, tt, "bracketed paste", func() bool { return c.s.Term.Modes().BracketedPaste })
	snippet, err := a.saveSnippet(assets.Snippet{Name: "insert"}, "echo 中文\\n\r\n")
	if err != nil {
		t.Fatal(err)
	}
	shell.take()
	c.tv.offset, c.tv.selected = 3, true
	if err := a.sendSnippet(snippet.ID, false); err != nil {
		t.Fatal(err)
	}
	if a.focusReq != c.id {
		t.Fatal("insert did not request terminal focus")
	}
	if err := a.sendSnippet(snippet.ID, false); !errors.Is(err, errSnippetBusy) {
		t.Fatalf("in-flight write was not refused: %v", err)
	}
	// Opening a search after the click must not let completion steal focus.
	a.finding, a.focusReq = true, 0
	deadline := time.Now().Add(5 * time.Second)
	for c.tv.snippetSending {
		a.drain()
		if time.Now().After(deadline) {
			t.Fatal("snippet write did not complete")
		}
		time.Sleep(time.Millisecond)
	}
	if a.focusReq != 0 {
		t.Fatal("completion stole focus from search")
	}
	a.finding = false
	var got string
	want := "\x1b[200~echo 中文\\n\x1b[201~"
	waitFor(t, tt, "snippet bytes", func() bool { got += shell.take(); return len(got) >= len(want) })
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if c.tv.offset != 0 || c.tv.selected {
		t.Fatal("successful insert did not clear scroll/selection")
	}

	// A nonterminal focused pane cannot fall back to the last terminal.
	c.kind = layout.Files
	if err := a.sendSnippet(snippet.ID, true); !errors.Is(err, errSnippetNoTerminal) {
		t.Fatalf("files target: %v", err)
	}
	c.kind = layout.Terminal
	c.s.Term.WriteString("\x1b[?2004l")
	unsafe, err := a.saveSnippet(assets.Snippet{Name: "unsafe"}, "a\nb")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.sendSnippet(unsafe.ID, false); !errors.Is(err, errSnippetUnsafePaste) {
		t.Fatalf("unsafe insert: %v", err)
	}
	if extra := shell.take(); extra != "" {
		t.Fatalf("rejected actions wrote %q", extra)
	}
	c.s.Close()
	if err := a.sendSnippet(snippet.ID, true); !errors.Is(err, errSnippetDisconnected) {
		t.Fatalf("disconnected target: %v", err)
	}
}

type snippetBusyTarget struct{ term *vt.Terminal }

func (p snippetBusyTarget) Terminal() *vt.Terminal  { return p.term }
func (snippetBusyTarget) Resize(int, int)           {}
func (snippetBusyTarget) Write([]byte) (int, error) { return 0, session.ErrInputBusy }

func TestSnippetBusyInputIsVisible(t *testing.T) {
	v := newTermView(snippetBusyTarget{}, 13)
	var reported error
	v.inputError = func(err error) { reported = err }
	v.send([]byte("key"))
	if !errors.Is(reported, errSnippetBusy) {
		t.Fatalf("ordinary input busy was not reported: %v", reported)
	}
}

func TestSendSnippetNoTerminal(t *testing.T) {
	a, _ := newTestApp(t)
	if err := a.sendSnippet("missing", false); !errors.Is(err, errSnippetNoTerminal) {
		t.Fatalf("no terminal: %v", err)
	}
}

func TestSendSnippetFreezesPane(t *testing.T) {
	srvA, shellA := startScript(t, "ready A")
	srvB, shellB := startScript(t, "ready B")
	a, tt := newTestApp(t)
	first := connected(t, a, tt, srvA)
	firstTab := a.ws.Active
	hostB := addHost(t, a, srvB, "box B", "secret")
	a.known.Add(srvB.Addr, srvB.HostKey.PublicKey())
	a.Connect(hostB.ID, openAt{})
	second := a.focused()
	waitFor(t, tt, "second connection", func() bool { return second.s.Status().State == session.Connected })
	snippet, err := a.saveSnippet(assets.Snippet{Name: "target"}, "echo selected")
	if err != nil {
		t.Fatal(err)
	}
	shellA.take()
	shellB.take()
	if err := a.sendSnippet(snippet.ID, true); err != nil {
		t.Fatal(err)
	}
	// The worker may run after this switch, but the destination is already bound.
	a.ws.Active, a.focusReq = firstTab, 0
	if a.focused() != first {
		t.Fatal("test did not switch panes")
	}
	waitFor(t, tt, "bound write completion", func() bool { return !second.tv.snippetSending })
	var got string
	waitFor(t, tt, "selected pane bytes", func() bool { got += shellB.take(); return len(got) >= len("echo selected\r") })
	if got != "echo selected\r" {
		t.Fatalf("selected pane got %q", got)
	}
	if wrong := shellA.take(); wrong != "" {
		t.Fatalf("newly focused pane received %q", wrong)
	}
	if a.focused() != first {
		t.Fatal("completion changed focused pane")
	}
}
