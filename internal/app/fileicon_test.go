package app

import (
	"image"
	"image/color"
	"path/filepath"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

func TestFileIconsListAndMenus(t *testing.T) {
	for _, mode := range []theme.Mode{theme.Light, theme.Dark} {
		t.Run(string(mode), func(t *testing.T) {
			a, tt, cn, dir := localPane(t, "src/inside.txt", "main.go", "package.json", "README.md", "Dockerfile", "archive.zip", "mystery.zzzyard", "settings.toml")
			s := a.Settings()
			s.Theme = mode
			if err := a.ApplySettings(s); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			tt.Frame()
			var hues uint8
			for _, name := range []string{"src", "main.go", "package.json", "README.md", "Dockerfile", "archive.zip"} {
				n, colors := fileIconColors(tt.Image(), fileIconBounds(t, tt, name))
				hues |= colors
				if n < 4 {
					t.Errorf("%s icon has only %d colored pixels", name, n)
				}
			}
			if hues&(hues-1) == 0 {
				t.Fatal("file icons share a single tint")
			}
			assertFileIconVariant(t, tt.Image(), fileIconBounds(t, tt, "settings.toml"), mode)
			unknown := fileIconBounds(t, tt, "mystery.zzzyard")
			// The default file is deliberately gray; require visible artwork.
			background := color.NRGBAModel.Convert(tt.Image().At(unknown.Min.X, unknown.Min.Y)).(color.NRGBA)
			visible := 0
			for y := unknown.Min.Y; y < unknown.Max.Y; y++ {
				for x := unknown.Min.X; x < unknown.Max.X; x++ {
					p := color.NRGBAModel.Convert(tt.Image().At(x, y)).(color.NRGBA)
					if max(p.R, background.R)-min(p.R, background.R) > 20 {
						visible++
					}
				}
			}
			if visible < 4 {
				t.Fatalf("default file icon is empty: %d visible pixels", visible)
			}
			r := fileIconBounds(t, tt, "main.go")
			before := tt.Image()
			x, y := nameAt(t, tt, "main.go")
			tt.ClickAt(x, y)
			tt.Frame()
			if !cn.fs.local.chosen.Has("main.go") {
				t.Fatal("mouse click did not select file")
			}
			after := tt.Image()
			stable := 0
			for y := r.Min.Y; y < r.Max.Y; y++ {
				for x := r.Min.X; x < r.Max.X; x++ {
					b := color.NRGBAModel.Convert(before.At(x, y)).(color.NRGBA)
					if saturatedFilePixel(b) && b == color.NRGBAModel.Convert(after.At(x, y)).(color.NRGBA) {
						stable++
					}
				}
			}
			if stable < 4 {
				t.Fatalf("selected icon lost its original colors: %d stable pixels", stable)
			}
			saveBrandScreenshot(t, tt, "files-selected-"+string(mode))
			tt.Advance(time.Second)
			if err := tt.RightClick("main.go"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			mustClick(t, tt, "Get Info")
			tt.Frame()
			if cn.fs.info == nil || !cn.fs.infoOpen {
				t.Fatal("file info did not open")
			}
			saveBrandScreenshot(t, tt, "files-info-"+string(mode))
			cn.fs.infoOpen = false
			tt.Frame()
			entries := func() []menuEntry { return a.crumbEntries(cn, false, dir, "", locales.In("en")) }
			a.openContext(450, 150, entries)
			tt.Frame()
			tt.Frame()
			// Only the menu's leading image column is inside this rectangle.
			assertFileIconVariant(t, tt.Image(), image.Rect(463, 155, 479, 155+len(entries())*28), mode)
			saveBrandScreenshot(t, tt, "files-menu-"+string(mode))
			// The menu is above the file list; click its actual row.
			tt.ClickAt(500, 150+5+14)
			tt.Frame()
			if cn.fs.local.dir != filepath.Join(dir, "src") {
				t.Fatalf("directory menu opened %q", cn.fs.local.dir)
			}
			if !tt.HasText("inside.txt") {
				t.Fatal("directory menu did not list its contents")
			}
		})
	}
}

// Find returns the accessible table row; its leading icon is 16 DIPs.
func fileIconBounds(t *testing.T, tt *ui.Tester, name string) image.Rectangle {
	t.Helper()
	r := mustFind(t, tt, name)
	return image.Rect(int(r.X+10), int(r.Y+r.H/2-8), int(r.X+26), int(r.Y+r.H/2+8))
}

func saturatedFilePixel(c color.NRGBA) bool {
	return max(c.R, c.G, c.B)-min(c.R, c.G, c.B) > 30
}

func fileIconColors(img image.Image, r image.Rectangle) (n int, hues uint8) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			p := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			if saturatedFilePixel(p) {
				n++
				switch max(p.R, p.G, p.B) {
				case p.R:
					hues |= 1
				case p.G:
					hues |= 2
				case p.B:
					hues |= 4
				}
			}
		}
	}
	return n, hues
}

// TOML's T has a distinct official foreground in each theme.
func assertFileIconVariant(t *testing.T, img image.Image, r image.Rectangle, mode theme.Mode) {
	t.Helper()
	want, other := color.NRGBA{R: 0x45, G: 0x5a, B: 0x64, A: 255}, color.NRGBA{R: 0xcf, G: 0xd8, B: 0xdc, A: 255}
	if mode == theme.Dark {
		want, other = other, want
	}
	matched := 0
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			pixel := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			if pixel == other {
				t.Fatalf("%s icon uses opposite theme color %v at %d,%d", mode, other, x, y)
			}
			if pixel == want {
				matched++
			}
		}
	}
	if matched < 4 {
		t.Fatalf("%s TOML icon has only %d pixels of theme color %v in %v", mode, matched, want, r)
	}
}
