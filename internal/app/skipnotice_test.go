package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Licoy/sshyard/internal/sftpsvc"
	"github.com/Licoy/sshyard/internal/sshtest"
	"github.com/egoist/mygo/ui"
)

func TestShownName(t *testing.T) {
	for in, want := range map[string]string{
		"plain.txt":             "plain.txt",
		`..\..\win.txt`:         `..\..\win.txt`,
		"a\nb\r\tc":             `a\nb\r\tc`,
		"esc\x1b[31m\x7f\u0085": `esc\x1b[31m\x7f\x85`,
		"\u202egpj.exe":         `\u202egpj.exe`,
		"\u2066x\u2069\u200f":   `\u2066x\u2069\u200f`,
		"line\u2028sep":         `line\u2028sep`,
		"bad\xffutf8":           `bad\xffutf8`,
		"日本語 ok":                "日本語 ok",
	} {
		if got := shownName(in); got != want {
			t.Errorf("shownName(%q) = %q, want %q", in, got, want)
		}
	}
}

const unsafeNote = "”: the name contains characters that aren’t safe on some systems; skipped"

func skipFS() *sshtest.FakeFS {
	return &sshtest.FakeFS{
		Dirs: map[string][]sshtest.FakeEntry{
			"/":  {{Name: "d", Dir: true}, {Name: "gone", Dir: true}},
			"/d": {{Name: "ok.txt", Size: 2}, {Name: "a\nb.txt", Size: 1}, {Name: "\u202egpj.exe:", Size: 1}},
		},
		Files: map[string][]byte{"/d/ok.txt": []byte("ok")},
	}
}

func download(t *testing.T, a *App, tt *ui.Tester, name string) {
	t.Helper()
	if err := tt.RightClick(name); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(a, tt, "Download"); err != nil {
		t.Fatal(err)
	}
}

// Skipped names are quoted, all on one line, nothing in them reordering
// the text.
func TestSkippedNoticeEscapesNames(t *testing.T) {
	a, tt, fc, _, _ := hostilePane(t, skipFS())
	waitFor(t, tt, "listing", func() bool { return tt.HasText("d") })
	download(t, a, tt, "d")
	waitFor(t, tt, "notice", func() bool { return tt.HasText("Left out of the download: 2") })
	for _, want := range []string{`“d/a\nb.txt` + unsafeNote, `“d/\u202egpj.exe:` + unsafeNote} {
		if !tt.HasText(want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
	for _, s := range tt.Texts() {
		if strings.ContainsAny(s, "\n\u202e") {
			t.Errorf("shown as is: %q", s)
		}
	}
	if len(fc.fs.skipped) != 2 {
		t.Fatalf("%+v", fc.fs.skipped)
	}
}

// The notice is of the last download: a download that cannot be planned
// clears it, as does cancelling the question about files already there.
func TestSkippedNoticeCleared(t *testing.T) {
	t.Run("plan fails", func(t *testing.T) {
		a, tt, fc, _, _ := hostilePane(t, skipFS())
		waitFor(t, tt, "listing", func() bool { return tt.HasText("gone") })
		download(t, a, tt, "d")
		waitFor(t, tt, "notice", func() bool { return tt.HasText("Left out of the download: 2") })
		fc.fs.transfers.Wait()
		download(t, a, tt, "gone")
		waitFor(t, tt, "failed", func() bool { return fc.fs.remote.err != "" || fc.fs.local.err != "" })
		tt.Frame()
		if tt.HasText("Left out of the download") || fc.fs.skipped != nil {
			t.Fatalf("old notice still shown: %q", tt.Texts())
		}
	})
	t.Run("conflict canceled", func(t *testing.T) {
		a, tt, fc, _, local := hostilePane(t, skipFS())
		os.MkdirAll(filepath.Join(local, "d"), 0o755)
		os.WriteFile(filepath.Join(local, "d", "ok.txt"), []byte("mine"), 0o644)
		waitFor(t, tt, "listing", func() bool { return tt.HasText("d") })
		download(t, a, tt, "d")
		waitFor(t, tt, "question", func() bool { return fc.fs.conflictOpen && tt.HasText("Left out of the download: 2") })
		mustClick(t, tt, "Cancel")
		tt.Frame()
		if tt.HasText("Left out of the download") || fc.fs.skipped != nil {
			t.Fatalf("notice still shown: %q", tt.Texts())
		}
		if n := len(fc.fs.transfers.List()); n != 0 {
			t.Fatalf("%d queued", n)
		}
	})
}

// A plan that fails in the same frame a finished transfer is first
// noticed keeps its error. That frame lists the folders again, and the
// refresh must not clear a failure that is not a listing failure.
func TestPlanErrorSurvivesTransferRefresh(t *testing.T) {
	a, tt, fc, _, _ := hostilePane(t, skipFS())
	waitFor(t, tt, "listing", func() bool { return tt.HasText("d") })
	download(t, a, tt, "d")
	waitFor(t, tt, "notice", func() bool { return tt.HasText("Left out of the download: 2") })
	waitFor(t, tt, "downloaded", func() bool {
		ts := fc.fs.transfers.List()
		return len(ts) == 1 && ts[0].State == sftpsvc.Done
	})
	fc.fs.done = 0
	a.post(func() {
		fc.fs.skipped = nil
		a.filesErr(fc, false, errors.New("sftp: /gone: file does not exist"))
	})
	tt.Frame()
	if fc.fs.local.err == "" || !tt.HasText("sftp: /gone: file does not exist") {
		t.Fatalf("plan error cleared by the transfer refresh: %q", tt.Texts())
	}
	if tt.HasText("Left out of the download") || fc.fs.skipped != nil {
		t.Fatalf("old notice still shown: %q", tt.Texts())
	}
}
