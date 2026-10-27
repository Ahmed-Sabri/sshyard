package app

import (
	"image"
	"testing"

	"github.com/Licoy/sshyard/internal/settings"
	"github.com/egoist/mygo/ui"
)

// alphaAt is the alpha of the window's pixel at the DIP x, y.
func alphaAt(img *image.RGBA, x, y float32) uint8 {
	return img.RGBAAt(int(x), int(y)).A
}

// TestMaterials: on the window's material, the sidebar with the title bar
// and the content each show it or their own fill, as the appearance
// settings say; without one, both are opaque and the choice is off.
func TestMaterials(t *testing.T) {
	a, tt := newTestApp(t)
	tt.SetVibrancy(true)
	tt.Frame()
	sidebar, content := func() (float32, float32) { return 40, 300 }, func() (float32, float32) { return 700, 400 }
	check := func(chrome, main bool) {
		t.Helper()
		img := tt.Image()
		if img == nil {
			t.Fatal("no image")
		}
		x, y := sidebar()
		if got := alphaAt(img, x, y) < 255; got != chrome {
			t.Errorf("sidebar translucent %v, want %v (alpha %d)", got, chrome, alphaAt(img, x, y))
		}
		x, y = content()
		if got := alphaAt(img, x, y) < 255; got != main {
			t.Errorf("content translucent %v, want %v (alpha %d)", got, main, alphaAt(img, x, y))
		}
	}
	// The defaults: the sidebar on the material, the content opaque.
	check(true, false)

	s := a.Settings()
	s.ChromeMaterial, s.ContentMaterial = settings.MaterialOpaque, settings.MaterialTranslucent
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.clearContent {
		t.Fatal("the content is not translucent")
	}
	check(false, true)

	a.openSettings(prefAppearance)
	tt.Frame()
	for _, s := range []string{"Translucency", "Sidebar & Title Bar", "Content Area", "Translucent", "Opaque"} {
		if !tt.HasText(s) {
			t.Errorf("missing %q: %q", s, tt.Texts())
		}
	}
	if tt.HasText("Translucency is available on macOS only.") {
		t.Error("the choice is off on the material")
	}

	tt.SetVibrancy(false)
	tt.Frame()
	if a.clearContent {
		t.Error("translucent content without a material")
	}
	if textCount(tt, "Translucency is available on macOS only.") != 2 {
		t.Errorf("no note: %q", tt.Texts())
	}
}

// TestRoundRows: the lists that choose a row light it, and the row under
// the pointer, in a rounded shape.
func TestRoundRows(t *testing.T) {
	ex := &dockerExec{}
	srv := startMetricSrv(t, ex.run)
	a, tt := newTestApp(t)
	tt.SetSize(1400, 900)
	cn := connected(t, a, tt, srv)
	a.inspector = inspectDocker
	tt.Frame()
	waitFor(t, tt, "the containers", func() bool { return tt.HasText("web") })
	a.docker.view = 1
	tt.Frame()
	a.inspector = inspectUsage
	tt.Frame()
	lists := map[string]*ui.ListState{"containers": &a.docker.ctrList, "images": &a.docker.imgList, "processes": &a.usage.list}
	mustClick(t, tt, "Open Files for This Host")
	tt.Frame()
	for _, o := range a.conns {
		if o.fs != nil && o != cn {
			lists["local files"], lists["remote files"] = &o.fs.local.list, &o.fs.remote.list
		}
	}
	if len(lists) != 5 {
		t.Fatalf("lists %v", len(lists))
	}
	for name, l := range lists {
		if !l.RoundRows {
			t.Errorf("%s: square rows", name)
		}
	}
}
