package app

import (
	"math"
	"runtime"
	"strings"

	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/internal/vt"
	"github.com/egoist/mygo/ui"
)

// TerminalView is SSHYard's own native terminal widget: it draws a
// vt.Terminal's grid with the ui Painter, glyph by glyph in its cells,
// and turns keys, text, the input method's composition, the pointer and
// the wheel into what the program on the other end reads. No web view
// and no HTML terminal are involved.

// ptyTarget is what a terminal view drives: the terminal it shows, and the
// program's input and window size.
type ptyTarget interface {
	Terminal() *vt.Terminal
	Write(p []byte) (int, error)
	Resize(cols, rows int)
}

const termPad = 10 // DIPs around the grid, inside the pane card

type shapeKey struct {
	text   string
	bold   bool
	italic bool
}

type termView struct {
	target ptyTarget
	// clear leaves the background to the pane's translucent fill, for a
	// content area on the window's material (settings.ContentMaterial)
	clear bool

	// font
	fontSize            float32
	family              string // the font family list; "" is "monospace"
	scale               float32
	cellW, cellH, base  float32 // DIPs, whole device pixels
	fonts               [4]ui.Font
	shaped              map[shapeKey][]ui.Glyph
	cols, rows          int
	sized               bool
	first               int // absolute line of the top row last drawn
	offset              int // lines scrolled into the scrollback
	scrollAcc           float32
	selA, selB          vt.Pos
	selecting, selected bool
	preedit             string
	swallow             bool

	// mouse reports
	held      bool // a button press was reported, its release not yet
	heldBtn   vt.MouseBtn
	lastMouse vt.Pos // the cell of the last report, for moves
	wheelAcc  float32
	focusSeen int8 // 0 unknown, 1 focused, -1 not

	// find
	query      string
	matches    []vt.Match
	current    int
	findGen    uint64
	findQuery  string
	findActive bool
}

func newTermView(t ptyTarget, fontSize float32) *termView {
	return &termView{target: t, fontSize: fontSize, shaped: map[shapeKey][]ui.Glyph{}}
}

// metrics measures the cells of the terminal font at a scale, again
// when its size or family changes.
func (v *termView) metrics(scale float32) {
	if scale <= 0 {
		scale = 1
	}
	fam := v.family
	if fam == "" {
		fam = "monospace"
	}
	if v.cellW > 0 && v.scale == scale && v.fonts[0].Size == v.fontSize && v.fonts[0].Family == fam {
		return
	}
	v.scale = scale
	for i := range v.fonts {
		f := ui.Font{Family: fam, Size: v.fontSize, Weight: 400}
		if i&1 != 0 {
			f.Weight = 700
		}
		f.Italic = i&2 != 0
		v.fonts[i] = f
	}
	m := v.fonts[0].Metrics()
	adv := float32(0)
	for _, g := range ui.Shape(asciiPrintable, v.fonts[0]) {
		adv = max(adv, g.Advance)
	}
	if adv <= 0 {
		adv = v.fontSize * 0.6
	}
	h := m.Ascent + m.Descent + m.LineGap
	if h <= 0 {
		h = v.fontSize * 1.25
		m.Ascent = v.fontSize
	}
	cw := max(float32(math.Ceil(float64(adv*scale-0.05))), 1)
	ch := max(float32(math.Ceil(float64(h*scale-0.05))), 1)
	v.cellW, v.cellH = cw/scale, ch/scale
	v.base = float32(math.Round(float64((ch-(m.Ascent+m.Descent)*scale)/2+m.Ascent*scale))) / scale
	clear(v.shaped)
}

const asciiPrintable = " !\"#$%&'()*+,-./0123456789:;<=>?@ABCDEFGHIJKLMNOPQRSTUVWXYZ[\\]^_`abcdefghijklmnopqrstuvwxyz{|}~"

// gridSize is how many cells fit in w×h DIPs.
func (v *termView) gridSize(w, h float32) (int, int) {
	if v.cellW == 0 {
		return 80, 24
	}
	cols := int((w - 2*termPad) / v.cellW)
	rows := int((h - 2*termPad) / v.cellH)
	return max(cols, 2), max(rows, 1)
}

// cellAt is the cell under a point in the element's box.
func (v *termView) cellAt(x, y float32) vt.Pos {
	if v.cellW == 0 {
		return vt.Pos{Line: v.first}
	}
	col := int(math.Floor(float64((x - termPad) / v.cellW)))
	row := int(math.Floor(float64((y - termPad) / v.cellH)))
	col = max(0, min(col, v.cols-1))
	row = max(0, min(row, v.rows-1))
	return vt.Pos{Line: v.first + row, Col: col}
}

func (v *termView) shape(text string, bold, italic bool) []ui.Glyph {
	k := shapeKey{text, bold, italic}
	if g, ok := v.shaped[k]; ok {
		return g
	}
	if len(v.shaped) > 4096 {
		clear(v.shaped)
	}
	i := 0
	if bold {
		i |= 1
	}
	if italic {
		i |= 2
	}
	g := ui.Shape(text, v.fonts[i])
	v.shaped[k] = g
	return g
}

// selection returns the selection in order.
func (v *termView) selection() (vt.Pos, vt.Pos, bool) {
	if !v.selected {
		return vt.Pos{}, vt.Pos{}, false
	}
	a, b := v.selA, v.selB
	if b.Less(a) {
		a, b = b, a
	}
	return a, b, true
}

func (v *termView) inSelection(line, col int) bool {
	a, b, ok := v.selection()
	if !ok {
		return false
	}
	p := vt.Pos{Line: line, Col: col}
	return !p.Less(a) && !b.Less(p)
}

// SelectedText is the text selected, or "".
func (v *termView) SelectedText() string {
	a, b, ok := v.selection()
	if !ok {
		return ""
	}
	return v.target.Terminal().Text(a, b)
}

// updateFind recomputes the matches of the query as the output changes.
func (v *termView) updateFind(query string, active bool) {
	t := v.target.Terminal()
	gen := t.Generation()
	if !active || query == "" {
		v.matches, v.findQuery, v.findActive = nil, "", false
		return
	}
	if v.findActive && gen == v.findGen && query == v.findQuery {
		return
	}
	v.matches = t.Find(query, false)
	v.findGen, v.findQuery, v.findActive = gen, query, true
	if v.current >= len(v.matches) {
		v.current = max(len(v.matches)-1, 0)
	}
}

// showMatch scrolls match i into view.
func (v *termView) showMatch(i int) {
	if i < 0 || i >= len(v.matches) {
		return
	}
	v.current = i
	v.offset = v.target.Terminal().OffsetFor(v.matches[i].Line, v.offset)
}

func (v *termView) matchAt(line, col int) int {
	for i, m := range v.matches {
		start := m.Line*max(v.cols, 1) + m.Col
		p := line*max(v.cols, 1) + col
		if p >= start && p < start+m.Len {
			if i == v.current {
				return 2
			}
			return 1
		}
	}
	return 0
}

// paint draws the grid, the selection, the matches, the cursor and the
// composition.
func (v *termView) paint(p *ui.Painter, r ui.Rect, pal *theme.Terminal, focused bool) {
	v.metrics(p.Scale())
	cols, rows := v.gridSize(r.W, r.H)
	if !v.sized || cols != v.cols || rows != v.rows {
		v.cols, v.rows, v.sized = cols, rows, true
		v.target.Resize(cols, rows)
	}
	t := v.target.Terminal()
	snap := t.Snapshot(v.offset)
	v.offset, v.first = snap.Offset, snap.First
	if !v.clear {
		p.Fill(r, pal.Background, 0)
	}
	s := v.scale
	ox := float32(math.Round(float64((r.X+termPad)*s))) / s
	oy := float32(math.Round(float64((r.Y+termPad)*s))) / s
	cw, ch := v.cellW, v.cellH
	matching := len(v.matches) > 0

	for y, line := range snap.Lines {
		ry := oy + float32(y)*ch
		abs := snap.First + y
		// backgrounds
		for x := 0; x < len(line.Cells) && x < snap.Cols; x++ {
			c := line.Cells[x]
			_, bg, set := cellColors(c, pal)
			switch {
			case v.inSelection(abs, x):
				bg, set = pal.Selection, true
			case matching:
				switch v.matchAt(abs, x) {
				case 1:
					bg, set = pal.Match, true
				case 2:
					bg, set = pal.MatchCurrent, true
				}
			}
			if set {
				w := float32(max(int(c.Width), 1))
				p.Fill(ui.Rect{X: ox + float32(x)*cw, Y: ry, W: cw * w, H: ch}, bg, 0)
			}
		}
		// text, in runs of one style whose glyphs go in their cells
		x := 0
		for x < len(line.Cells) && x < snap.Cols {
			c := line.Cells[x]
			if c.Width == 0 || c.Rune == 0 || c.Rune == ' ' {
				x++
				continue
			}
			fg, _, _ := cellColors(c, pal)
			bold, italic := c.Attr&vt.Bold != 0, c.Attr&vt.Italic != 0
			var sb strings.Builder
			var colOf []int
			start := x
			for x < len(line.Cells) && x < snap.Cols {
				d := line.Cells[x]
				if d.Width == 0 {
					x++
					continue
				}
				if d.Rune == 0 || d.Rune == ' ' {
					break
				}
				f2, _, _ := cellColors(d, pal)
				if f2 != fg || (d.Attr&vt.Bold != 0) != bold || (d.Attr&vt.Italic != 0) != italic {
					break
				}
				sb.WriteRune(d.Rune)
				colOf = append(colOf, x)
				// what joins the rune is drawn in its cell
				for _, cr := range d.Comb {
					sb.WriteRune(cr)
					colOf = append(colOf, x)
				}
				x++
			}
			if sb.Len() == 0 {
				x = start + 1
				continue
			}
			g := v.shape(sb.String(), bold, italic)
			placed := make([]ui.Glyph, len(g))
			copy(placed, g)
			placeGlyphs(placed, g, colOf, line.Cells, cw)
			p.Glyphs(placed, ox, ry+v.base, fg)
			// decorations
			if d := line.Cells[start].Attr; d&(vt.Underline|vt.Strike) != 0 {
				end := colOf[len(colOf)-1] + 1
				x0, x1 := ox+float32(start)*cw, ox+float32(end)*cw
				th := max(1/s, 1)
				if d&vt.Underline != 0 {
					p.Fill(ui.Rect{X: x0, Y: ry + v.base + 2/s, W: x1 - x0, H: th}, fg, 0)
				}
				if d&vt.Strike != 0 {
					p.Fill(ui.Rect{X: x0, Y: ry + v.base - v.fontSize*0.3, W: x1 - x0, H: th}, fg, 0)
				}
			}
		}
	}

	// cursor
	if snap.CursorVisible && snap.CursorY >= 0 && snap.CursorY < snap.Rows && v.preedit == "" {
		cx := ox + float32(min(snap.CursorX, snap.Cols-1))*cw
		cy := oy + float32(snap.CursorY)*ch
		w := cw
		var under vt.Cell
		if line := snap.Lines[snap.CursorY]; snap.CursorX < len(line.Cells) {
			under = line.Cells[snap.CursorX]
			if under.Width == 2 {
				w = 2 * cw
			}
		}
		cur := ui.Rect{X: cx, Y: cy, W: w, H: ch}
		switch {
		case !focused:
			p.Stroke(cur, pal.Cursor, 0, 1/s)
		case snap.CursorStyle == vt.CursorUnderline:
			p.Fill(ui.Rect{X: cx, Y: cy + ch - 2, W: w, H: 2}, pal.Cursor, 0)
		case snap.CursorStyle == vt.CursorBar:
			p.Fill(ui.Rect{X: cx, Y: cy, W: max(2/s, 1.5), H: ch}, pal.Cursor, 0)
		default:
			p.Fill(cur, pal.Cursor, 0)
			if under.Rune > ' ' {
				g := v.shape(under.Text(), under.Attr&vt.Bold != 0, under.Attr&vt.Italic != 0)
				placed := make([]ui.Glyph, len(g))
				copy(placed, g)
				shift := float32(0)
				for i := range placed {
					if i > 0 && g[i].Cluster == g[i-1].Cluster {
						placed[i].X = g[i].X + shift // a mark on the base
						continue
					}
					placed[i].X = max((w-placed[i].Advance)/2, 0)
					shift = placed[i].X - g[i].X
				}
				p.Glyphs(placed, cx, cy+v.base, pal.CursorText)
			}
		}
	}

	// the input method's composition, at the cursor
	if v.preedit != "" && snap.CursorY >= 0 && snap.CursorY < snap.Rows {
		cx := ox + float32(snap.CursorX)*cw
		cy := oy + float32(snap.CursorY)*ch
		g := v.shape(v.preedit, false, false)
		w := float32(0)
		for _, gl := range g {
			w = max(w, gl.X+gl.Advance)
		}
		p.Fill(ui.Rect{X: cx, Y: cy, W: w, H: ch}, pal.Background, 0)
		p.Glyphs(g, cx, cy+v.base, pal.Foreground)
		p.Fill(ui.Rect{X: cx, Y: cy + ch - 1, W: w, H: 1}, pal.Foreground, 0)
	}

	// where the view is in the scrollback
	if snap.Offset > 0 && snap.Scrollback > 0 {
		total := float32(snap.Scrollback + snap.Rows)
		h := max(r.H*float32(snap.Rows)/total, 16)
		top := r.Y + (r.H-h)*float32(snap.Scrollback-snap.Offset)/float32(snap.Scrollback)
		p.Fill(ui.Rect{X: r.X + r.W - 6, Y: top, W: 4, H: h}, pal.Foreground.Alpha(0.35), 2)
	}
}

// placeGlyphs puts the glyphs g of a run of cells in their cells:
// colOf is the column of each rune of the run. Each cluster is centered
// in its cell(s), as monospace fallback fonts are often narrower or
// wider; the glyphs after the first of a cluster (combining marks) keep
// their place on it.
func placeGlyphs(placed, g []ui.Glyph, colOf []int, cells []vt.Cell, cw float32) {
	shift := float32(0)
	for i := range placed {
		ci := placed[i].Cluster
		if ci < 0 || ci >= len(colOf) {
			continue
		}
		if i > 0 && g[i].Cluster == g[i-1].Cluster {
			placed[i].X = g[i].X + shift
			continue
		}
		col := colOf[ci]
		cellsW := cw * float32(max(int(cells[col].Width), 1))
		dx := (cellsW - placed[i].Advance) / 2
		// A ligature over several cells starts at the first.
		if end := ci + max(placed[i].Runes, 1) - 1; end < len(colOf) && colOf[end] != col || placed[i].Advance > cellsW {
			dx = 0
		}
		placed[i].X = float32(col)*cw + max(dx, 0)
		shift = placed[i].X - g[i].X
	}
}

// caret is the cursor's cell in the element's box, where the input
// method's window goes.
func (v *termView) caret() ui.Rect {
	t := v.target.Terminal()
	snap := t.Snapshot(v.offset)
	return ui.Rect{X: termPad + float32(snap.CursorX)*v.cellW, Y: termPad + float32(snap.CursorY)*v.cellH, W: max(v.cellW, 1), H: max(v.cellH, 1)}
}

func (v *termView) send(b []byte) {
	if len(b) == 0 {
		return
	}
	v.offset = 0
	v.selected = false
	v.target.Write(b)
}

// reports says whether the program gets the mouse: it asked, the view is
// at the bottom, and the user is not holding the key that keeps the mouse
// for selecting text (Shift, or ⌥ too on macOS).
func (v *termView) reports(m vt.Modes, mods ui.Modifiers) bool {
	if m.Mouse == vt.MouseOff || v.offset != 0 {
		return false
	}
	if mods&ui.Shift != 0 || runtime.GOOS == "darwin" && mods&ui.Alt != 0 {
		return false
	}
	return true
}

// report sends a mouse event at cell p of the view to the program.
func (v *termView) report(m vt.Modes, btn vt.MouseBtn, act vt.MouseAction, p vt.Pos, mods ui.Modifiers) {
	ev := vt.MouseEvent{Button: btn, Action: act, Col: p.Col, Row: p.Line - v.first,
		Shift: mods&ui.Shift != 0, Alt: mods&ui.Alt != 0 && runtime.GOOS != "darwin", Ctrl: mods&ui.Ctrl != 0}
	if b := vt.EncodeMouse(m, ev); b != nil {
		v.target.Write(b)
	}
}

// focusChanged reports the focus coming to or leaving the pane to a
// program that asked for it (1004).
func (v *termView) focusChanged(focused bool) {
	seen := int8(-1)
	if focused {
		seen = 1
	}
	if v.focusSeen == seen {
		return
	}
	first := v.focusSeen == 0
	v.focusSeen = seen
	if first {
		return
	}
	if b := vt.EncodeFocus(v.target.Terminal().Modes(), focused); b != nil {
		v.target.Write(b)
	}
}

// Paste sends text as pasted.
func (v *termView) Paste(text string) {
	if text == "" {
		return
	}
	v.send(pasteBytes(text, v.target.Terminal().Modes()))
}

// onlyControl reports text made of control characters, which keys the
// view encoded itself already sent.
func onlyControl(s string) bool {
	for _, r := range s {
		if r >= ' ' && r != 0x7f {
			return false
		}
	}
	return true
}

// clipboard is the window's clipboard, through ui.Services.
type clipboard interface {
	ReadClipboard() string
	WriteClipboard(string)
}

// input handles an event; it reports whether it took it.
func (v *termView) input(ev ui.InputEvent, cb clipboard) bool {
	t := v.target.Terminal()
	switch ev.Kind {
	case ui.InputKeyDown:
		v.swallow = false
		mods := ev.Mods
		if isAppShortcut(ev.Key, mods) {
			return false
		}
		// Ctrl+Shift+C and V copy and paste on Linux and Windows, where
		// Ctrl+C is the interrupt.
		if runtime.GOOS != "darwin" && mods&(ui.Ctrl|ui.Shift) == ui.Ctrl|ui.Shift && mods&ui.Alt == 0 {
			switch ev.Key {
			case ui.KeyC:
				if s := v.SelectedText(); s != "" {
					cb.WriteClipboard(s)
				}
				v.swallow = true
				return true
			case ui.KeyV:
				v.Paste(cb.ReadClipboard())
				v.swallow = true
				return true
			}
		}
		if mods == ui.Shift {
			switch ev.Key {
			case ui.KeyPageUp:
				v.offset += max(v.rows-1, 1)
				return true
			case ui.KeyPageDown:
				v.offset = max(v.offset-max(v.rows-1, 1), 0)
				return true
			case ui.KeyInsert:
				v.Paste(cb.ReadClipboard())
				return true
			}
		}
		if b, ok := EncodeKey(ev.Key, mods, t.Modes()); ok {
			v.send(b)
			v.swallow = mods&(ui.Ctrl|ui.Alt) != 0 && typesText(ev.Key)
			return true
		}
		if mods&ui.Super != 0 {
			return false // the app's shortcuts
		}
		return typesText(ev.Key)
	case ui.InputKeyUp:
		v.swallow = false
		return true
	case ui.InputText:
		if v.swallow {
			v.swallow = false
			return true
		}
		v.preedit = ""
		if ev.Text == "" || onlyControl(ev.Text) {
			return true
		}
		v.send([]byte(strings.ReplaceAll(ev.Text, "\n", "\r")))
		return true
	case ui.InputCompose:
		v.preedit = ev.Text
		return true
	case ui.InputCommand:
		switch ev.Text {
		case "copy", "cut":
			if s := v.SelectedText(); s != "" {
				cb.WriteClipboard(s)
			}
			return true
		case "paste", "pasteAndMatchStyle":
			v.Paste(cb.ReadClipboard())
			return true
		case "selectAll":
			snap := t.Snapshot(0)
			v.selA = vt.Pos{Line: snap.First - snap.Scrollback, Col: 0}
			v.selB = vt.Pos{Line: snap.First + snap.Rows - 1, Col: snap.Cols - 1}
			v.selected = true
			return true
		}
		return false
	case ui.InputPointerDown:
		if m := t.Modes(); v.reports(m, ev.Mods) {
			btn := vt.MouseLeft
			switch ev.Button {
			case 1:
				btn = vt.MouseRight
			case 2:
				btn = vt.MouseMiddle
			}
			p := v.cellAt(ev.X, ev.Y)
			v.held, v.heldBtn, v.lastMouse = true, btn, p
			v.report(m, btn, vt.MousePress, p, ev.Mods)
			return true
		}
		if ev.Button == 1 {
			return false // the context menu
		}
		if ev.Button == 2 {
			v.Paste(cb.ReadClipboard())
			return true
		}
		p := v.cellAt(ev.X, ev.Y)
		switch {
		case ev.Clicks == 2:
			v.selA, v.selB = t.WordAt(p)
			v.selected = true
		case ev.Clicks >= 3:
			v.selA, v.selB = vt.Pos{Line: p.Line}, vt.Pos{Line: p.Line, Col: max(v.cols-1, 0)}
			v.selected = true
		case ev.Mods&ui.Shift != 0 && v.selected:
			v.selB = p
		default:
			v.selA, v.selB = p, p
			v.selected = false
		}
		v.selecting = true
		return true
	case ui.InputPointerMove:
		if v.held || !v.selecting {
			m := t.Modes()
			btn := vt.MouseNone
			if v.held {
				btn = v.heldBtn
			}
			if p := v.cellAt(ev.X, ev.Y); (v.held || v.reports(m, ev.Mods)) && m.Mouse != vt.MouseOff {
				if p != v.lastMouse {
					v.lastMouse = p
					v.report(m, btn, vt.MouseMotion, p, ev.Mods)
				}
				return v.held || m.Mouse == vt.MouseAny
			}
		}
		if v.selecting && ev.Button == 0 {
			p := v.cellAt(ev.X, ev.Y)
			if p != v.selB || p != v.selA {
				v.selB = p
				v.selected = v.selA != v.selB || v.selected
			}
			return true
		}
		return false
	case ui.InputPointerUp:
		if v.held {
			v.held = false
			m := t.Modes()
			v.report(m, v.heldBtn, vt.MouseRelease, v.cellAt(ev.X, ev.Y), ev.Mods)
			return true
		}
		if v.selecting {
			v.selecting = false
			return true
		}
		return false
	case ui.InputScroll:
		if v.cellH == 0 {
			return false
		}
		if m := t.Modes(); v.reports(m, ev.Mods) {
			// One report a notch, or three lines of a precise scroll.
			n := 0
			if ev.Precise {
				v.wheelAcc += ev.DY
				n = int(v.wheelAcc / (3 * v.cellH))
				v.wheelAcc -= float32(n) * 3 * v.cellH
			} else if ev.DY > 0 {
				n = 1
			} else if ev.DY < 0 {
				n = -1
			}
			btn := vt.MouseWheelDown
			if n < 0 {
				btn = vt.MouseWheelUp
			}
			p := v.cellAt(ev.X, ev.Y)
			for range abs(n) {
				v.report(m, btn, vt.MousePress, p, ev.Mods)
			}
			return true
		}
		lines := 0
		if ev.Precise {
			v.scrollAcc += ev.DY
			lines = int(v.scrollAcc / v.cellH)
			v.scrollAcc -= float32(lines) * v.cellH
		} else if ev.DY > 0 {
			lines = 3
		} else if ev.DY < 0 {
			lines = -3
		}
		if lines == 0 {
			return true
		}
		m := t.Modes()
		if m.AltScreen {
			// Full-screen programs scroll with the arrows, as xterm's
			// alternateScroll.
			k := ui.KeyDown
			if lines < 0 {
				k = ui.KeyUp
			}
			b, _ := EncodeKey(k, 0, m)
			for range abs(lines) {
				v.target.Write(b)
			}
			return true
		}
		v.offset = max(v.offset-lines, 0)
		return true
	}
	return false
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
