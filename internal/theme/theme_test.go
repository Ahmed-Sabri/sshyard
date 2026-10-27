package theme

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestBrandIsFixed(t *testing.T) {
	want := ui.Color{R: 0x19, G: 0x84, B: 0xFC, A: 0xFF}
	if Brand != want {
		t.Fatalf("Brand = %+v, want %+v", Brand, want)
	}
	for _, tk := range []Tokens{LightTokens(), DarkTokens()} {
		if tk.Accent != want {
			t.Errorf("dark=%v accent %+v, want brand", tk.Dark, tk.Accent)
		}
		if tk.Terminal.Cursor != want {
			t.Errorf("dark=%v cursor %+v, want brand", tk.Dark, tk.Terminal.Cursor)
		}
	}
}

func TestModes(t *testing.T) {
	cases := []struct {
		m      Mode
		system bool
		dark   bool
	}{
		{Auto, false, false}, {Auto, true, true},
		{Light, true, false}, {Light, false, false},
		{Dark, false, true}, {Dark, true, true},
	}
	for _, c := range cases {
		if got := c.m.IsDark(c.system); got != c.dark {
			t.Errorf("%s.IsDark(%v) = %v", c.m, c.system, got)
		}
		if got := For(c.m, c.system).Dark; got != c.dark {
			t.Errorf("For(%s,%v).Dark = %v", c.m, c.system, got)
		}
	}
	if Mode("sepia").Valid() || !Auto.Valid() || len(Modes) != 3 {
		t.Error("mode validity")
	}
}

func TestContrast(t *testing.T) {
	for _, tk := range []Tokens{LightTokens(), DarkTokens()} {
		if c := Contrast(tk.Text, tk.Background); c < 7 {
			t.Errorf("dark=%v text contrast %.2f < 7", tk.Dark, c)
		}
		if c := Contrast(tk.TextMuted, tk.Background); c < 4.5 {
			t.Errorf("dark=%v muted contrast %.2f < 4.5", tk.Dark, c)
		}
		if c := Contrast(tk.AccentText, tk.Accent); c < 3 {
			t.Errorf("dark=%v accent text contrast %.2f < 3", tk.Dark, c)
		}
		if c := Contrast(tk.Terminal.Foreground, tk.Terminal.Background); c < 7 {
			t.Errorf("dark=%v terminal contrast %.2f < 7", tk.Dark, c)
		}
		if tk.Dark != (Luminance(tk.Background) < 0.2) {
			t.Errorf("dark=%v background luminance %.2f", tk.Dark, Luminance(tk.Background))
		}
	}
}

func TestApply(t *testing.T) {
	base := ui.Theme{Font: "Inter", Accent: ui.Hex("#ff0000")}
	th := DarkTokens().Apply(base)
	if !th.Dark || th.Accent != Brand || th.Font != "Inter" || th.Radius != RadiusControl {
		t.Errorf("Apply: %+v", th)
	}
	if base.Accent == Brand {
		t.Error("Apply changed its base")
	}
}

// TestScales: radii nest (each step holds the one below with room), the
// spacing scale is the 4-DIP grid and the type scale rises.
func TestScales(t *testing.T) {
	radii := []float32{RadiusSmall, RadiusControl, RadiusCard, RadiusSheet}
	for i := 1; i < len(radii); i++ {
		if radii[i] <= radii[i-1] {
			t.Errorf("radius %d (%v) not above %v", i, radii[i], radii[i-1])
		}
	}
	if Capsule < 100 {
		t.Errorf("capsule radius %v rounds no ends", Capsule)
	}
	if Space(1) != 4 || Space(6) != 24 || Space(2.5) != 10 {
		t.Errorf("space scale %v %v %v", Space(1), Space(6), Space(2.5))
	}
	types := []float32{Caption, Footnote, Body, Title3, Title2, Title1}
	for i := 1; i < len(types); i++ {
		if types[i] <= types[i-1] {
			t.Errorf("type size %d (%v) not above %v", i, types[i], types[i-1])
		}
	}
	for _, tk := range []Tokens{LightTokens(), DarkTokens()} {
		if tk.FontSize != Body || tk.Radius != RadiusControl || tk.Spacing != SpaceUnit {
			t.Errorf("dark=%v metrics %v %v %v", tk.Dark, tk.FontSize, tk.Radius, tk.Spacing)
		}
	}
}

// TestSurfaces: the layers of each appearance stay apart in the right
// order, the translucent ones are translucent and the text reads on every
// opaque layer.
func TestSurfaces(t *testing.T) {
	for _, tk := range []Tokens{LightTokens(), DarkTokens()} {
		for name, c := range map[string]ui.Color{"glass": tk.Glass, "glassedge": tk.GlassEdge, "line": tk.Line,
			"hover": tk.Hover, "press": tk.Press, "fill": tk.Fill} {
			if c.A == 0 || c.A == 0xFF {
				t.Errorf("dark=%v %s alpha %d, want translucent", tk.Dark, name, c.A)
			}
		}
		if tk.Hover.A >= tk.Press.A {
			t.Errorf("dark=%v hover not lighter than press", tk.Dark)
		}
		for name, c := range map[string]ui.Color{"sidebar": tk.Sidebar, "canvas": tk.Canvas, "inset": tk.Inset, "terminal": tk.Terminal.Background} {
			if c.A != 0xFF {
				t.Errorf("dark=%v %s not opaque", tk.Dark, name)
			}
			if r := Contrast(tk.Text, c); r < 7 {
				t.Errorf("dark=%v text on %s %.2f < 7", tk.Dark, name, r)
			}
			if r := Contrast(tk.TextMuted, c); r < 4.5 {
				t.Errorf("dark=%v muted on %s %.2f < 4.5", tk.Dark, name, r)
			}
		}
		// The sidebar and the inset cards stand apart from the canvas.
		if tk.Sidebar == tk.Canvas || tk.Inset == tk.Canvas {
			t.Errorf("dark=%v layers merge", tk.Dark)
		}
		if tk.Lift.Blur <= tk.Card.Blur || tk.Lift.Color.A <= tk.Card.Color.A {
			t.Errorf("dark=%v lift shadow not above card shadow", tk.Dark)
		}
	}
}

func TestTint(t *testing.T) {
	l, d := LightTokens(), DarkTokens()
	if l.Tint("web-01") != l.Tint("web-01") {
		t.Error("tint not stable")
	}
	seen := map[ui.Color]bool{}
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"} {
		seen[l.Tint(n)] = true
		if d.Tint(n) == l.Tint(n) {
			t.Errorf("tint of %q same light and dark", n)
		}
	}
	if len(seen) < 3 {
		t.Errorf("tints spread over %d colors", len(seen))
	}
}

func TestSidebarAndMaterialThemes(t *testing.T) {
	tk := LightTokens()
	base := *tk.Apply(ui.Theme{})
	if th := tk.SidebarTheme(base, false); th.Surface != tk.Sidebar || th.SurfaceHover != tk.Hover || th.Accent != Brand {
		t.Errorf("sidebar theme %+v", th)
	}
	if th := tk.SidebarTheme(base, true); th.Surface != ui.Transparent || th.Background != ui.Transparent {
		t.Error("clear sidebar theme keeps a fill")
	}
	if th := tk.Material(base); th.Background != ui.Transparent || th.Text != tk.Text {
		t.Error("material theme")
	}
}

func TestTintAt(t *testing.T) {
	l, d := LightTokens(), DarkTokens()
	for i := range Tints {
		if l.TintAt(i) != Tints[i][0] || d.TintAt(i) != Tints[i][1] {
			t.Fatalf("TintAt(%d) does not follow the mode", i)
		}
	}
	if l.TintAt(-1) != Tints[len(Tints)-1][0] || l.TintAt(len(Tints)) != Tints[0][0] {
		t.Fatal("TintAt does not wrap")
	}
}
