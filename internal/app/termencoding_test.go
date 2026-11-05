package app

import (
	"fmt"
	"testing"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/internal/vt"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

func TestOutputEncodingToolbar(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint("dark=", dark), func(t *testing.T) {
			srv, sh := startScript(t, "ready")
			a, tt := newTestApp(t)
			tt.SetDark(dark)
			m := locales.In("en")
			if tt.HasText(m.OutputEncodingTip(m.OutputEncodingUTF8())) {
				t.Fatal("encoding without terminal")
			}
			cn := connected(t, a, tt, srv)
			tt.Frame()
			r := mustFind(t, tt, m.OutputEncodingTip(m.OutputEncodingUTF8()))
			if r.Y < 730 || r.Y+r.H > 760 {
				t.Fatalf("not in 30px status bar: %v", r)
			}
			mustClick(t, tt, m.OutputEncodingTip(m.OutputEncodingUTF8()))
			gbk := mustFind(t, tt, m.OutputEncodingGBK())
			if gbk.Y < 0 || gbk.Y+gbk.H > r.Y || gbk.X+gbk.W > 1200 {
				t.Fatalf("menu did not flip inside window: %v chip %v", gbk, r)
			}
			if inset := gbk.X - r.X; inset < 12 || inset > 15 {
				t.Fatalf("empty icon gutter remains: label inset %v", inset)
			}
			mustClick(t, tt, m.OutputEncodingGBK())
			if cn.s.Term.OutputEncoding() != vt.EncodingGBK || !hasExact(tt, m.OutputEncodingGBK()) {
				t.Fatal("GBK choice was not applied")
			}
			// Selection returns focus to the terminal, with input still UTF-8.
			sh.take()
			tt.Type("中文")
			expectRead(t, tt, sh, "中文")
			tt.SetClipboard("粘贴")
			tt.Command("paste")
			expectRead(t, tt, sh, "粘贴")
			snippet, err := a.saveSnippet(assets.Snippet{Name: "snippet"}, "片段")
			if err != nil {
				t.Fatal(err)
			}
			if err := a.sendSnippet(snippet.ID, false); err != nil {
				t.Fatal(err)
			}
			expectRead(t, tt, sh, "片段")
			mustClick(t, tt, m.OutputEncodingTip(m.OutputEncodingGBK()))
			mustClick(t, tt, m.OutputEncodingUTF8())
			if cn.s.Term.OutputEncoding() != vt.EncodingUTF8 {
				t.Fatal("UTF-8 choice was not applied")
			}
			mustClick(t, tt, m.OutputEncodingTip(m.OutputEncodingUTF8()))
			mustClick(t, tt, "Settings")
			if a.menu.key != "" {
				t.Fatal("encoding menu remained open in settings")
			}
			if tt.HasText(m.OutputEncodingTip(m.OutputEncodingUTF8())) {
				t.Fatal("encoding shown in settings")
			}
			mustClick(t, tt, "Close Settings")
			if a.menu.key != "" || hasExact(tt, m.OutputEncodingGBK()) {
				t.Fatal("encoding menu reopened after settings")
			}
		})
	}
}

// Capture the real popover's bounds: Tester can locate text and labels but
// does not expose a query for an otherwise unnamed menu panel.
func TestOutputEncodingMenuLayout(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint("dark=", dark), func(t *testing.T) {
			a := &App{menu: dropMenu{focus: 0, sub: -1}}
			m := locales.In("en")
			open, chosen := true, false
			note := ""
			var menu ui.Rect
			tt := ui.NewTester(func(c *ui.Context) {
				anchor := ui.Box(c).Absolute().Left(300).Top(350).Size(80, 24)
				ui.PopoverBase(c, anchor, &open, func(panel ui.Element) {
					panel.Margin(4, 0, 0, 0)
					a.menuPanel(c, currentTokens(c), panel, []menuEntry{
						{Label: m.OutputEncodingUTF8(), Checked: true, Note: note},
						{Label: m.OutputEncodingGBK(), Do: func() { chosen, open = true, false }},
					}, 0, true)
					menu = panel.Bounds()
				})
			}, 500, 400)
			tt.SetDark(dark)
			tt.Frame()
			if menu.W < theme.Space(25) || menu.W > theme.Space(30) || menu.Y < 0 || menu.Y+menu.H > 350 {
				t.Fatalf("compact popover bounds: %v", menu)
			}
			for _, label := range []string{m.OutputEncodingUTF8(), m.OutputEncodingGBK()} {
				r := mustFind(t, tt, label)
				if r.X-menu.X < 12 || r.X-menu.X > 15 || r.W < 34 || r.X+r.W > menu.X+menu.W-12 {
					t.Fatalf("text %q clipped or padded incorrectly: %v menu %v", label, r, menu)
				}
			}
			if !tt.Focused(m.OutputEncodingUTF8()) {
				t.Fatal("first menu item did not take focus")
			}
			tt.Key(0, ui.KeyDown)
			tt.Key(0, ui.KeyEnter)
			if !chosen || open {
				t.Fatal("keyboard did not choose and dismiss compact menu")
			}
			// A multi-line choice keeps the original layout even when compact
			// was requested; its note must not adjust a missing icon element.
			note, open = m.OutputEncodingUTF8(), true
			tt.Frame()
			if menu.W < menuWidth {
				t.Fatalf("multi-line menu lost its original width: %v", menu)
			}
			if r := mustFind(t, tt, m.OutputEncodingGBK()); r.X-menu.X < 38 {
				t.Fatalf("multi-line menu lost its shared icon column: %v menu %v", r, menu)
			}
		})
	}
}

func TestOutputEncodingPaneScope(t *testing.T) {
	srv, _ := startScript(t, "ready")
	a, tt := newTestApp(t)
	first := connected(t, a, tt, srv)
	a.Connect(first.host.ID, openAt{pane: first.id, edge: layout.Right})
	second := a.focused()
	waitFor(t, tt, "second terminal", func() bool { return second.s.Status().State == session.Connected })
	tt.Frame()
	m := locales.In("en")
	if first.s.Term.OutputEncoding() != vt.EncodingUTF8 || second.s.Term.OutputEncoding() != vt.EncodingUTF8 {
		t.Fatal("new terminal not UTF-8")
	}
	mustClick(t, tt, m.OutputEncodingTip(m.OutputEncodingUTF8()))
	key := a.menu.key
	// Capture a menu callback while it belongs to the second pane, then change focus.
	var entries []menuEntry
	capture := ui.NewTester(func(c *ui.Context) { entries = a.outputEncodingEntries(c, second, m) }, 1, 1)
	capture.Frame()
	if !entries[0].Checked || entries[1].Checked {
		t.Fatal("default check is not UTF-8")
	}
	a.ws.Activate(first.id)
	a.focusReq = first.id
	tt.Frame()
	if tt.HasText(m.OutputEncodingGBK()) || a.menu.key != "" {
		t.Fatal("second pane menu leaked to first pane")
	}
	mustClick(t, tt, m.OutputEncodingTip(m.OutputEncodingUTF8()))
	if a.menu.key == key {
		t.Fatal("menu key shared across panes")
	}
	mustClick(t, tt, m.OutputEncodingTip(m.OutputEncodingUTF8()))
	entries[1].Do()
	capture.Frame()
	if entries[0].Checked || !entries[1].Checked {
		t.Fatal("GBK check did not follow selection")
	}
	if first.s.Term.OutputEncoding() != vt.EncodingUTF8 || second.s.Term.OutputEncoding() != vt.EncodingGBK || a.focusReq != second.id {
		t.Fatal("callback did not retain its pane")
	}
	// A later terminal starts at UTF-8 even while another pane uses GBK.
	a.Connect(first.host.ID, openAt{})
	fresh := a.focused()
	waitFor(t, tt, "fresh terminal", func() bool { return fresh.s.Status().State == session.Connected })
	if fresh.s.Term.OutputEncoding() != vt.EncodingUTF8 {
		t.Fatal("new terminal inherited another pane's encoding")
	}
	a.CloseSession(fresh.id)
	a.ws.Activate(second.id)
	tt.Frame()
	mustClick(t, tt, m.OutputEncodingTip(m.OutputEncodingGBK()))
	if a.menu.key != key {
		t.Fatal("menu key changed for the same pane")
	}
	// Switching panes by pointer dismisses the previous pane's menu.
	if a.menu.key == "" {
		t.Fatal("encoding menu did not open")
	}
	left := mustFind(t, tt, "Terminal box")
	tt.ClickAt(left.X+20, left.Y+20)
	tt.Frame()
	if a.menu.key != "" {
		t.Fatal("previous pane menu remained open")
	}
	if a.focused() != first || !hasExact(tt, m.OutputEncodingUTF8()) {
		t.Fatal("focus did not synchronize encoding label")
	}
	if _, err := a.OpenAI(first.host.ID, openAt{pane: first.id, edge: layout.Bottom}, first.id); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText(m.OutputEncodingTip(m.OutputEncodingUTF8())) {
		t.Fatal("encoding shown on AI")
	}
	a.ws.Activate(first.id)
	a.focusReq = first.id
	tt.Frame()
	mustClick(t, tt, m.OutputEncodingTip(m.OutputEncodingUTF8()))
	mustClick(t, tt, "Open Files on the Right")
	tt.Frame()
	if a.focused().kind != layout.Files || a.menu.key != "" || tt.HasText(m.OutputEncodingTip(m.OutputEncodingUTF8())) {
		t.Fatal("encoding shown on Files")
	}
}
