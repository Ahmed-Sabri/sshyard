package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestZoomFor(t *testing.T) {
	for _, c := range []struct {
		chosen                int
		text, desktop, device float32
		want                  float32
	}{
		{0, 1, 0, 0, 1},
		{0, 0, 0, 0, 1},
		{0, 1.25, 0, 0, 1.25},
		{125, 1.5, 0, 0, 1.25},
		// X11 at 150% that the toolkit draws at 1×, and at 2× for 200%.
		{0, 1, 1.5, 1, 1.5},
		{0, 1, 2, 2, 1},
		{0, 1, 1.4999, 1, 1.5},
		// The text size and the desktop's scale together.
		{0, 1.2, 1.5, 1, 1.8},
		{0, 9, 0, 0, 3},
		{0, 0.1, 0, 0, 0.5},
	} {
		if got := zoomFor(c.chosen, c.text, c.desktop, c.device); got != c.want {
			t.Errorf("zoomFor(%d, %v, %v, %v) = %v, want %v", c.chosen, c.text, c.desktop, c.device, got, c.want)
		}
	}
}

func TestParseXftDPI(t *testing.T) {
	for out, want := range map[string]float32{
		"Xft.antialias:\t1\nXft.dpi:\t144\nXft.hinting:\t1\n": 1.5,
		"Xft.dpi: 192":        2,
		"Xcursor.size:\t24\n": 0,
		"Xft.dpi:\tlarge\n":   0,
		"Xft.dpi:\t9600\n":    0,
		"":                    0,
	} {
		if got := parseXftDPI(out); got != want {
			t.Errorf("parseXftDPI(%q) = %v, want %v", out, got, want)
		}
	}
}

// TestInterfaceZoom: the window zooms with the system's text size, or as
// the settings choose; the shortcuts step the zoom and reset it, which
// persists, and the appearance settings choose it.
func TestInterfaceZoom(t *testing.T) {
	a, tt := newTestApp(t)
	tt.SetSize(1200, 800)
	a.openSettings(prefAppearance)
	tt.Frame()
	width := func() float32 {
		t.Helper()
		r, ok := tt.Find("Interface zoom")
		if !ok {
			t.Fatalf("no zoom row: %q", tt.Texts())
		}
		return r.W
	}
	if a.zoom.now != 1 {
		t.Fatalf("zoom %v at the system's defaults", a.zoom.now)
	}
	w := width()
	if !tt.HasText("Follow system") {
		t.Errorf("the zoom does not follow the system: %q", tt.Texts())
	}

	tt.SetPreferences(ui.Preferences{TextScale: 1.25})
	tt.Frame()
	if a.zoom.now != 1.25 {
		t.Errorf("zoom %v with the text at 125%%", a.zoom.now)
	}
	if got := width(); got < w*1.2 {
		t.Errorf("text %v wide at 125%%, %v at 100%%", got, w)
	}

	s := a.Settings()
	s.UIZoom = 150
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.zoom.now != 1.5 || !tt.HasText("150%") {
		t.Errorf("zoom %v as chosen 150%%: %q", a.zoom.now, tt.Texts())
	}

	tt.Key(appMods, ui.KeyEqual)
	tt.Frame()
	if a.settings.UIZoom != 175 || a.zoom.now != 1.75 {
		t.Errorf("zoomed in to %d (%v)", a.settings.UIZoom, a.zoom.now)
	}
	if !tt.HasText("Interface zoom: 175%") {
		t.Errorf("no toast: %q", tt.Texts())
	}
	tt.Key(appMods, ui.KeyMinus)
	tt.Key(appMods, ui.KeyMinus)
	tt.Frame()
	if a.settings.UIZoom != 125 {
		t.Errorf("zoomed out to %d", a.settings.UIZoom)
	}
	b, _ := os.ReadFile(filepath.Join(a.p.DataDir, "settings.json"))
	if !strings.Contains(string(b), `"uiZoom": 125`) {
		t.Errorf("settings file %s", b)
	}
	tt.Key(appMods, ui.Key0)
	tt.Frame()
	if a.settings.UIZoom != 0 || a.zoom.now != 1.25 {
		t.Errorf("reset to %d (%v)", a.settings.UIZoom, a.zoom.now)
	}

	// The smallest steps no further.
	s = a.Settings()
	s.UIZoom = 80
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	a.stepZoom(-1)
	if a.settings.UIZoom != 80 {
		t.Errorf("zoomed out past the smallest to %d", a.settings.UIZoom)
	}
}
