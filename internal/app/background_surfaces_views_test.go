package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

type backgroundView struct {
	a      *App
	tt     *ui.Tester
	labels []string
	check  func()
}

// Exercise full window layouts as well as the isolated surface pixel tests;
// content must keep readable glyphs and working controls over the image.
func TestBackgroundContentViews(t *testing.T) {
	for _, tc := range []struct {
		name string
		make func(*testing.T) backgroundView
	}{
		{"settings", backgroundSettingsView},
		{"host", backgroundHostView},
		{"monitor", backgroundMonitorView},
		{"ai", backgroundAIView},
		{"snippets", backgroundSnippetsView},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := tc.make(t)
			checkBackgroundView(t, v, tc.name)
			if v.check != nil {
				v.check()
			}
		})
	}
}

func checkBackgroundView(t *testing.T, v backgroundView, name string) {
	t.Helper()
	renderBackgroundFixture(v.a)
	v.a.settings.Background.ChromeOpacity = 0
	v.a.settings.Background.ContentOpacity = 0
	v.tt.SetVibrancy(false)
	for _, dark := range []bool{false, true} {
		v.tt.SetDark(dark)
		v.tt.Move(1, 1)
		v.tt.Frame()
		v.tt.Frame()
		for _, label := range v.labels {
			assertBackgroundBehindText(t, v.tt, label, dark)
		}
		saveBackgroundShot(t, v.tt, fmt.Sprintf("content-%s-dark-%v", name, dark))
	}
	v.a.settings.Background.ContentOpacity = 80
	v.tt.Frame()
	saveBackgroundShot(t, v.tt, "content-"+name+"-default-dark")
	v.a.settings.Background.ContentOpacity = 0
	v.tt.Frame()
}

func assertBackgroundBehindText(t *testing.T, tt *ui.Tester, label string, dark bool) {
	t.Helper()
	r := mustFind(t, tt, label)
	img := tt.Image()
	visible, backdrop := 0, 0
	tok := theme.For(theme.Auto, dark)
	// Stay within the text's own layout rectangle: this excludes card borders,
	// rounded corners and neighboring action buttons without fixed coordinates.
	for y := int(r.Y) + 1; y < int(r.Y+r.H)-1; y++ {
		for x := int(r.X) + 1; x < int(r.X+r.W)-1; x++ {
			p := img.RGBAAt(x, y)
			if (int(p.R)-int(p.B) > 130 || int(p.B)-int(p.R) > 130) && p.G < 90 {
				backdrop++
			} else if nearColor(p.R, p.G, p.B, tok.Text.R, tok.Text.G, tok.Text.B) || nearColor(p.R, p.G, p.B, tok.TextMuted.R, tok.TextMuted.G, tok.TextMuted.B) {
				visible++
			}
		}
	}
	if backdrop < 10 {
		t.Errorf("%q content blocks the background at %v", label, r)
	}
	if visible < 2 {
		t.Errorf("%q has no readable foreground glyphs at %v", label, r)
	}
}

func backgroundSettingsView(t *testing.T) backgroundView {
	a, tt := newTestApp(t)
	a.openSettings(prefAppearance)
	tt.Frame()
	return backgroundView{a, tt, []string{"Theme"}, func() {
		mustClick(t, tt, "Back")
		if a.page != pageWorkspace {
			t.Fatal("settings Back did not restore the workspace")
		}
	}}
}

func backgroundHostView(t *testing.T) backgroundView {
	a, tt := newTestApp(t)
	h := savedHost(t, a, "background host", "10.0.0.8")
	a.selected = h.ID
	a.openPanel(inspectHost)
	tt.Frame()
	return backgroundView{a, tt, []string{"Jump hosts", "Open panes"}, func() {
		m := locales.In("en")
		mustClick(t, tt, m.AddFavorite())
		saved, _ := a.store.Get(h.ID)
		if !saved.Favorite {
			t.Fatal("host favorite action failed over background")
		}
		mustClick(t, tt, m.EditHost())
		tt.Frame()
		if !a.editing || !tt.Focused(m.FieldName()) {
			t.Fatal("host edit sheet did not open and focus the name")
		}
		// A sheet remains an opaque, readable overlay even at zero content opacity.
		r := mustFind(t, tt, m.FieldName())
		p := tt.Image().RGBAAt(int(r.X+1), int(r.Y+1))
		if int(p.R)-int(p.B) > 130 || int(p.B)-int(p.R) > 130 {
			t.Fatalf("host sheet does not retain its own readable surface: %v", p)
		}
		saveBackgroundShot(t, tt, "content-host-edit-sheet")
		mustClick(t, tt, "Cancel")
		if a.editing {
			t.Fatal("host edit Cancel did not close the sheet")
		}
	}}
}

func backgroundMonitorView(t *testing.T) backgroundView {
	ex := &frameExec{frames: linuxFrames(true)}
	srv := startMetricSrv(t, ex.run)
	a, tt := newTestApp(t)
	tt.SetSize(1400, 1400)
	connected(t, a, tt, srv)
	waitSample(t, tt)
	a.openPanel(inspectMonitor)
	tt.Frame()
	return backgroundView{a, tt, []string{"Operating system", "Average usage", "RAM", "eth0"}, nil}
}

func backgroundAIView(t *testing.T) backgroundView {
	a, tt := newTestApp(t)
	h := savedHost(t, a, "AI host", "10.0.0.8")
	id, err := a.OpenAI(h.ID, openAt{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	attachment, err := attachData("background-notes.txt", []byte("Disk usage notes"))
	if err != nil {
		t.Fatal(err)
	}
	a.conns[id].ai.items = []aiItem{
		{kind: "user", text: "Check the attached notes", files: []*aiAttach{attachment}},
		{kind: "text", text: "Read the disk usage\n\n```sh\ndf -h\n```\n"},
	}
	tt.Frame()
	attachmentMeta := attachment.format() + " · " + humanSize(int64(len(attachment.data)))
	return backgroundView{a, tt, []string{"Read the disk usage", attachmentMeta}, func() {
		mustClick(t, tt, "Copy")
		if tt.Clipboard() != "df -h" {
			t.Fatalf("AI code copy: %q", tt.Clipboard())
		}
	}}
}

func backgroundSnippetsView(t *testing.T) backgroundView {
	a, tt := newTestApp(t)
	n, err := a.saveSnippet(assets.Snippet{Name: "background snippet"}, "echo background\npwd")
	if err != nil {
		t.Fatal(err)
	}
	a.openPanel(inspectSnippets)
	a.snippets.selected = n.ID
	tt.Frame()
	return backgroundView{a, tt, []string{"echo background\npwd"}, func() {
		m := locales.In("en")
		mustClick(t, tt, m.SnippetMore(n.Name))
		tt.Frame()
		if !tt.HasText(m.Edit()) || !tt.HasText(m.Delete()) {
			t.Fatal("snippet action menu lost its actions")
		}
		r := mustFind(t, tt, m.Edit())
		p := tt.Image().RGBAAt(int(r.X+2), int(r.Y+2))
		if int(p.R)-int(p.B) > 130 || int(p.B)-int(p.R) > 130 {
			t.Fatalf("snippet menu lost its own readable surface: %v", p)
		}
		saveBackgroundShot(t, tt, "content-snippet-menu")
		mustClick(t, tt, m.Edit())
		tt.Frame()
		if a.snippets.form == nil || a.snippets.form.content != "echo background\npwd" {
			t.Fatal("snippet edit lost the selected body")
		}
		mustClick(t, tt, m.Cancel())
	}}
}

func TestBackgroundManagerCards(t *testing.T) {
	docker, tmux := &dockerExec{}, &tmuxExec{shell: "idle"}
	srv := startMetricSrv(t, func(ch ssh.Channel, cmd string) int {
		if strings.Contains(cmd, "command -v docker") {
			return docker.run(ch, cmd)
		}
		return tmux.run(ch, cmd)
	})
	a, tt := newTestApp(t)
	tt.SetSize(1400, 900)
	connected(t, a, tt, srv)
	a.openPanel(inspectDocker)
	waitFor(t, tt, "container details", func() bool { return tt.HasText("nginx:1.27") })
	a.docker.ctrSel = 0
	checkBackgroundView(t, backgroundView{a, tt, []string{"0.0.0.0:80->80/tcp"}, nil}, "docker")
	a.closePanel(inspectDocker)
	a.openPanel(inspectTmux)
	waitFor(t, tt, "tmux session", func() bool { return tt.HasText("/srv/api") })
	checkBackgroundView(t, backgroundView{a, tt, []string{"/srv/api"}, nil}, "tmux")
	mustClick(t, tt, locales.In("en").TmuxShowWindows())
	tt.Frame()
	if !tt.HasText("0: zsh") {
		t.Fatal("tmux expand action did not reveal windows")
	}
}
