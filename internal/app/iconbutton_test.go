package app

import (
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

func TestIconButton(t *testing.T) {
	var clicks, disabledClicks int
	var label string
	view := func(c *ui.Context) {
		m := locales.In(c.Locale())
		label = m.SplitRight()
		ui.Row(c).Padding(40).Gap(8).Children(func() {
			if (IconButton{Icon: icons.SplitRight, Label: m.SplitRight()}).Clicked(c, "split") {
				clicks++
			}
			if (IconButton{Icon: icons.Delete, Label: m.DeleteHost(), Disabled: true}).Clicked(c, "del") {
				disabledClicks++
			}
			IconButton{Icon: icons.Star, On: icons.StarOn, Label: m.RemoveFavorite(), Active: true, Toggle: true}.Show(c, "fav")
		})
	}
	tt := ui.NewTester(view, 400, 200)
	tt.SetLocale("en")
	tt.Frame()
	if err := tt.Click("Split Right"); err != nil {
		t.Fatal(err)
	}
	if clicks != 1 {
		t.Errorf("%d clicks", clicks)
	}
	tt.Click("Delete Host")
	if disabledClicks != 0 {
		t.Error("a disabled IconButton reported a click")
	}
	count := func(s string) (n int) {
		for _, x := range tt.Texts() {
			if x == s {
				n++
			}
		}
		return n
	}
	// No text: the label is only the accessible name, until the tooltip.
	before := count("Split Right")
	b, ok := tt.Find("Split Right")
	if !ok {
		t.Fatal("no element labeled Split Right")
	}
	tt.Move(b.X+b.W/2, b.Y+b.H/2)
	tt.Advance(2 * time.Second)
	tt.Frame()
	if count("Split Right") <= before {
		t.Errorf("no tooltip: %q", tt.Texts())
	}

	tt.SetLocale("zh-Hans")
	tt.Frame()
	zh := locales.In("zh-Hans").SplitRight()
	if label != zh || zh == "Split Right" {
		t.Fatalf("label %q", label)
	}
	if _, ok := tt.Find(zh); !ok {
		t.Errorf("no element labeled %q", zh)
	}
	tt.Move(0, 0)
	tt.Advance(time.Second)
	tt.Move(b.X+b.W/2, b.Y+b.H/2)
	tt.Advance(2 * time.Second)
	tt.Frame()
	if count(zh) < 2 {
		t.Errorf("no Chinese tooltip: %q", tt.Texts())
	}
}

// Every icon an IconButton uses is a Remix Icon "-line" icon, or the
// "-fill" one for an active state.
func TestIconNames(t *testing.T) {
	for _, ic := range []*icons.Icon{icons.Add, icons.Close, icons.SplitRight, icons.SplitDown, icons.Folder, icons.Terminal, icons.Settings, icons.Star, icons.Edit, icons.Delete, icons.Upload, icons.Download, icons.GoUp, icons.Refresh, icons.NewFolder, icons.Reconnect, icons.Clear, icons.Search, icons.Forward, icons.Run, icons.Robot, icons.Stop, icons.Back, icons.Info, icons.Monitor, icons.Shortcuts, icons.Quick, icons.Import, icons.Light, icons.Dark, icons.LocalDisk, icons.Privacy, icons.LocalShell, icons.Transfers} {
		if n := ic.Name; len(n) < 5 || n[len(n)-5:] != "-line" {
			t.Errorf("%s is not a -line icon", n)
		}
	}
	for _, ic := range []*icons.Icon{icons.SettingsOn, icons.StarOn, icons.FolderOn, icons.TerminalOn, icons.RunOn, icons.RobotOn, icons.InfoOn, icons.MonitorOn, icons.PrivacyOn} {
		if n := ic.Name; n[len(n)-5:] != "-fill" {
			t.Errorf("%s is not a -fill icon", n)
		}
	}
}
