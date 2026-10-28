package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

func TestSnippetUIManageWithoutTerminal(t *testing.T) {
	a, tt := newTestApp(t)
	m := locales.In("en")
	mustClick(t, tt, m.Snippets())
	tt.Frame()
	if !tt.HasText(m.SnippetNoTarget()) {
		t.Fatal(tt.Texts())
	}
	mustClick(t, tt, m.SnippetNew())
	tt.Frame()
	if !tt.Focused(m.FieldName()) {
		t.Fatal("name did not autofocus")
	}
	tt.Type("deploy")
	tt.Frame()
	if a.snippets.form.meta.Name != "deploy" {
		t.Fatal("autofocus input failed")
	}
	// TextInput owns the bound values, invoke the save handler directly.
	a.snippets.form.meta.Group = "work"
	a.snippets.form.content = "echo first\necho second"
	if !a.saveSnippetForm(m) {
		t.Fatal(a.snippets.form.err)
	}
	tt.Frame()
	n, _ := a.store.Snippet(a.snippets.selected)
	if n.Name != "deploy" || n.SendEnter {
		t.Fatal(n)
	}
	mustClick(t, tt, m.SnippetPreview(n.Name))
	mustClick(t, tt, m.SnippetPreview(n.Name))
	tt.Frame()
	if a.snippets.err != "" || !tt.HasText("echo first\necho second") {
		t.Fatal("row should preview without sending", tt.Texts())
	}
	mustClick(t, tt, m.SnippetMore(n.Name))
	tt.Frame()
	mustClick(t, tt, m.Edit())
	tt.Frame()
	mustClick(t, tt, m.SnippetImmediate())
	tt.Frame()
	if !a.snippets.form.meta.SendEnter || a.snippets.err != "" {
		t.Fatal("switch sent or failed to toggle")
	}
	a.snippets.form.meta.Name = "deploy edited"
	if !a.saveSnippetForm(m) {
		t.Fatal("edit save failed")
	}
	tt.Frame()
	n, _ = a.store.Snippet(n.ID)
	if !n.SendEnter || !tt.HasText(m.SnippetSendEnter()) {
		t.Fatal("mode invisible")
	}
	mustClick(t, tt, m.SnippetMore(n.Name))
	tt.Frame()
	mustClick(t, tt, m.SnippetDuplicate())
	tt.Frame()
	if a.snippets.form.meta.ID != "" || a.snippets.form.meta.SendEnter || a.snippets.form.content != "echo first\necho second" {
		t.Fatal("bad duplicate")
	}
	a.snippets.form.meta.Name = "copy"
	if !a.saveSnippetForm(m) {
		t.Fatal("copy save failed")
	}
	tt.Frame()
	if len(a.store.Snippets()) != 2 {
		t.Fatal("copy missing")
	}
	mustClick(t, tt, m.SnippetMore("copy"))
	tt.Frame()
	mustClick(t, tt, m.Delete())
	tt.Frame()
	if !tt.HasText(m.SnippetDeleteConfirm("copy")) {
		t.Fatal("no confirmation")
	}
	mustClick(t, tt, m.Delete())
	tt.Frame()
	if len(a.store.Snippets()) != 1 {
		t.Fatal("delete failed")
	}
}

func TestSnippetUISearchGroupMissingAndLanguages(t *testing.T) {
	a, tt := newTestApp(t)
	m := locales.In("en")
	n, err := a.saveSnippet(assets.Snippet{Name: "alpha", Group: "Ops"}, "echo private-body-needle")
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.saveSnippet(assets.Snippet{Name: "beta", Group: "Other", SendEnter: true}, "echo different")
	if err != nil {
		t.Fatal(err)
	}
	mustClick(t, tt, m.Snippets())
	tt.Frame()
	mustClick(t, tt, m.SnippetSearch())
	tt.Type("BODY-NEEDLE")
	tt.Frame()
	if !tt.HasText("alpha") || tt.HasText("beta") {
		t.Fatal("body search", tt.Texts())
	}
	tt.Key(ui.Super, ui.KeyA)
	tt.Type("Ops")
	tt.Frame()
	if !tt.HasText("alpha") || tt.HasText("beta") {
		t.Fatal("group search", tt.Texts())
	}
	tt.Key(ui.Super, ui.KeyA)
	tt.Type("ALPHA")
	tt.Frame()
	if !tt.HasText("alpha") || tt.HasText("beta") {
		t.Fatal("name search", tt.Texts())
	}
	tt.Key(ui.Super, ui.KeyA)
	tt.Type("")
	tt.Key(0, ui.KeyBackspace)
	tt.Frame()
	// Close and reopen search input so a fresh query can be set through its handler.
	a.inspector = ""
	tt.Frame()
	a.snippets.query = ""
	a.inspector = inspectSnippets
	tt.Frame()
	mustClick(t, tt, m.SnippetFilterGroup())
	tt.Frame()
	mustClick(t, tt, "Other")
	tt.Frame()
	if tt.HasText("alpha") || !tt.HasText("beta") {
		t.Fatal("group filter", tt.Texts())
	}
	a.snippets.group = ""
	a.snippets.selected = n.ID
	if err := a.kc.Delete(keychain.SnippetContent(n.ID)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText(m.SnippetMissing()) {
		t.Fatal("missing body not visible")
	}
	a.openSnippet(m, n, false)
	tt.Frame()
	if a.snippets.form == nil || a.snippets.form.content != "" {
		t.Fatal("missing content cannot be edited")
	}
	a.snippets.showing = false
	a.snippets.form = nil
	tt.Frame()
	for _, loc := range []string{"en", "zh-Hans"} {
		tt.SetLocale(loc)
		tt.SetDark(true)
		tt.SetSize(760, 560)
		tt.Frame()
		lm := locales.In(loc)
		for _, s := range []string{lm.SnippetNew(), lm.ClosePanel(), lm.SnippetSearch(), lm.SnippetMissing(), lm.SnippetInsertAction(n.Name)} {
			if !tt.HasText(s) {
				t.Errorf("%s lacks %s", loc, s)
			}
		}
		r := mustFind(t, tt, lm.SnippetNew())
		if r.X < 0 || r.X+r.W > 760 || r.Y+r.H > 560 {
			t.Fatalf("small window clips action: %+v", r)
		}
	}
}

func TestSnippetUIErrorsRedacted(t *testing.T) {
	for _, loc := range []string{"en", "zh-Hans"} {
		m := locales.In(loc)
		for _, err := range []error{errors.New("/private/path secret command"), &snippetStoreError{stage: "metadata", err: errors.New("secret body")}, &snippetStoreError{stage: "rollback", err: errors.New("secret path")}, errSnippetUnsafePaste, errSnippetSend} {
			got := snippetErrorText(m, err)
			if got == "" || strings.Contains(got, "secret") || strings.Contains(got, "/private") {
				t.Fatalf("unsafe %q", got)
			}
		}
	}
}

func TestSnippetUIFocusedTargetAndSend(t *testing.T) {
	spectatorSrv, spectatorShell := startScript(t, "ready")
	srv, shell := startScript(t, "\x1b[?2004hready")
	a, tt := newTestApp(t)
	spectatorHost := addHost(t, a, spectatorSrv, "spectator", "secret")
	a.known.Add(spectatorSrv.Addr, spectatorSrv.HostKey.PublicKey())
	a.Connect(spectatorHost.ID, openAt{})
	spectatorConn := a.focused()
	waitFor(t, tt, "spectator connected", func() bool { return spectatorConn.s.Status().State == session.Connected })
	cn := connected(t, a, tt, srv)
	m := locales.In("en")
	waitFor(t, tt, "bracketed paste", func() bool { return cn.s.Term.Modes().BracketedPaste })
	n, err := a.saveSnippet(assets.Snippet{Name: "send action"}, "echo UI marker")
	if err != nil {
		t.Fatal(err)
	}
	mustClick(t, tt, m.Snippets())
	tt.Frame()
	shell.take()
	spectatorShell.take()
	if !tt.HasText(m.SnippetTarget(a.pv(cn.host.Label()), "1")) {
		t.Fatal("target label missing", tt.Texts())
	}
	mustClick(t, tt, m.SnippetPreview(n.Name))
	mustClick(t, tt, m.SnippetPreview(n.Name))
	tt.Frame()
	if got := shell.take(); got != "" {
		t.Fatalf("selection sent %q", got)
	}
	mustClick(t, tt, m.SnippetInsertAction(n.Name))
	got := ""
	waitFor(t, tt, "UI snippet bytes", func() bool { got += shell.take(); return strings.Contains(got, "echo UI marker") })
	if !tt.Focused(m.Terminal() + " " + cn.host.Label()) {
		t.Fatal("focus did not return to terminal")
	}
	if got != "\x1b[200~echo UI marker\x1b[201~" || a.inspector != inspectSnippets {
		t.Fatalf("send %q inspector %q", got, a.inspector)
	}

	tt.Key(0, ui.KeyEnter)
	entered := ""
	waitFor(t, tt, "manual Enter after insert", func() bool { entered += shell.take(); return entered != "" })
	if entered != "\r" {
		t.Fatalf("manual Enter sent %q", entered)
	}
	waitFor(t, tt, "insert completed", func() bool { return !cn.tv.snippetSending })
	n.SendEnter = true
	if _, err := a.saveSnippet(n, "echo UI marker"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	mustClick(t, tt, m.SnippetSendAction(n.Name))
	immediate := ""
	wantImmediate := "\x1b[200~echo UI marker\x1b[201~\r"
	waitFor(t, tt, "immediate send bytes", func() bool { immediate += shell.take(); return len(immediate) >= len(wantImmediate) })
	if immediate != wantImmediate {
		t.Fatalf("immediate send %q", immediate)
	}
	if extra := spectatorShell.take(); extra != "" {
		t.Fatalf("unfocused terminal received %q", extra)
	}
	waitFor(t, tt, "immediate completed", func() bool { return !cn.tv.snippetSending })
	n.SendEnter = false
	if _, err := a.saveSnippet(n, "echo UI marker"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	// Render the panel alone while checking each pane kind, the terminal fixture
	// intentionally has no file/editor/AI backing state.
	panel := ui.NewTester(func(c *ui.Context) { a.snippetsPanel(c, m, currentTokens(c)) }, 360, 560)
	// Any nonterminal focused pane must disable sending without guessing a target.
	for _, kind := range []layout.Kind{layout.Files, layout.Editor, layout.AI} {
		cn.kind = kind
		panel.Frame()
		if a.snippetTarget() != nil || !panel.HasText(m.SnippetNoTarget()) {
			t.Fatalf("nonterminal %s has target", kind)
		}
		mustClick(t, panel, m.SnippetInsertAction(n.Name))
		panel.Frame()
		if extra := shell.take(); extra != "" {
			t.Fatalf("nonterminal sent %q", extra)
		}
	}
	cn.kind = layout.Terminal
}

func TestSnippetUIFilterFollowsSavedAndDeletedGroup(t *testing.T) {
	a, tt := newTestApp(t)
	m := locales.In("en")
	n, err := a.saveSnippet(assets.Snippet{Name: "move", Group: "old"}, "echo move")
	if err != nil {
		t.Fatal(err)
	}
	a.inspector = inspectSnippets
	a.snippets.group = "old"
	a.openSnippet(m, n, false)
	tt.Frame()
	a.snippets.form.meta.Group = "new"
	if !a.saveSnippetForm(m) {
		t.Fatal("save failed")
	}
	tt.Frame()
	if a.snippets.group != "new" || !tt.HasText("move") {
		t.Fatal("saved entry hidden")
	}
	a.snippets.deleteID = n.ID
	a.confirmSnippetDelete(m)
	tt.Frame()
	if a.snippets.group != "" {
		t.Fatal("stale group filter")
	}
}

func TestSnippetUIWelcomeActionsStayInsideContent(t *testing.T) {
	a, tt := newTestApp(t)
	m := locales.In("en")
	a.inspector = inspectSnippets
	tt.SetSize(820, 520)
	tt.Frame()
	left := a.sbWidth + 1
	right := float32(820 - railWidth - 360 - 2)
	for _, label := range []string{m.ImportSSHConfig(), m.QuickConnect(), m.NewHost()} {
		r := mustFind(t, tt, label)
		if r.W <= 0 || r.H <= 0 || r.X < left || r.X+r.W > right || r.Y < 40 || r.Y+r.H > 520-statusBarHeight {
			t.Fatalf("welcome action %q outside content [%v,%v]: %+v", label, left, right, r)
		}
	}
	for _, label := range []string{m.ImportSSHConfig(), m.QuickConnect(), m.NewHost()} {
		mustClick(t, tt, label)
		tt.Frame()
		if label == m.ImportSSHConfig() {
			if !a.importing {
				t.Fatal("import unavailable")
			}
			a.importing = false
			a.imp = nil
		}
		if label == m.QuickConnect() {
			if !a.quicking {
				t.Fatal("quick connect unavailable")
			}
			a.quicking = false
			a.quick = nil
		}
		if label == m.NewHost() {
			if !a.editing {
				t.Fatal("new host unavailable")
			}
			a.editing = false
			a.form = nil
		}
		tt.Frame()
	}
}
