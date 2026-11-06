package app

import (
	"time"

	"github.com/egoist/mygo/ui"
)

const cursorBlinkInterval = 500 * time.Millisecond

type cursorBlinkClock struct {
	at   time.Time
	x, y int
}

// showCursor schedules only the next blink edge, using the painter's clock
// so input and repaint-only frames share the same time source.
func (v *termView) showCursor(p *ui.Painter, focused bool) bool {
	snap := &v.snap
	visible := snap.CursorVisible && snap.CursorY >= 0 && snap.CursorY < snap.Rows && v.preedit == ""
	if !visible || !focused || !v.cursorBlink || !snap.CursorBlink {
		v.blink.at = time.Time{}
		return visible
	}
	now := p.Now()
	if v.blink.at.IsZero() || v.blink.x != snap.CursorX || v.blink.y != snap.CursorY {
		v.blink = cursorBlinkClock{at: now, x: snap.CursorX, y: snap.CursorY}
	}
	elapsed := now.Sub(v.blink.at)
	p.After(cursorBlinkInterval - elapsed%cursorBlinkInterval)
	return elapsed/cursorBlinkInterval%2 == 0
}
