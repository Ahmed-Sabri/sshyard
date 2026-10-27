package vt

import "strconv"

// MouseMode is which mouse events a program asked to receive.
type MouseMode uint8

const (
	MouseOff    MouseMode = iota
	MouseX10              // DECSET 9: presses only
	MouseNormal           // 1000: presses and releases
	MouseButton           // 1002: and moves while a button is held
	MouseAny              // 1003: and every move
)

// MouseBtn is the button of a mouse event.
type MouseBtn uint8

const (
	MouseLeft MouseBtn = iota
	MouseMiddle
	MouseRight
	MouseNone // a move with no button held
	MouseWheelUp
	MouseWheelDown
)

// MouseAction is what happened.
type MouseAction uint8

const (
	MousePress MouseAction = iota
	MouseRelease
	MouseMotion
)

// MouseEvent is a mouse event to report to the program, at a cell of
// the screen (0-based).
type MouseEvent struct {
	Button           MouseBtn
	Action           MouseAction
	Col, Row         int
	Shift, Alt, Ctrl bool
}

// EncodeMouse is what the program reads for ev under modes m, or nil
// when the modes do not ask for it. The default encoding cannot say a
// column or row past 223: such events are dropped, as xterm does; SGR
// (1006) has no limit.
func EncodeMouse(m Modes, ev MouseEvent) []byte {
	if m.Mouse == MouseOff || ev.Col < 0 || ev.Row < 0 {
		return nil
	}
	wheel := ev.Button == MouseWheelUp || ev.Button == MouseWheelDown
	switch ev.Action {
	case MouseRelease:
		if m.Mouse == MouseX10 || wheel {
			return nil
		}
	case MouseMotion:
		switch {
		case m.Mouse == MouseAny:
		case m.Mouse == MouseButton && ev.Button != MouseNone:
		default:
			return nil
		}
		if wheel {
			return nil
		}
	}
	var cb int
	switch ev.Button {
	case MouseLeft, MouseMiddle, MouseRight:
		cb = int(ev.Button)
	case MouseNone:
		if ev.Action != MouseMotion {
			return nil
		}
		cb = 3
	case MouseWheelUp:
		cb = 64
	case MouseWheelDown:
		cb = 65
	}
	if ev.Action == MouseMotion {
		cb += 32
	}
	if m.Mouse != MouseX10 {
		if ev.Shift {
			cb += 4
		}
		if ev.Alt {
			cb += 8
		}
		if ev.Ctrl {
			cb += 16
		}
	}
	if m.MouseSGR {
		final := byte('M')
		if ev.Action == MouseRelease {
			final = 'm'
		}
		b := append([]byte("\x1b[<"), strconv.Itoa(cb)...)
		b = append(b, ';')
		b = append(b, strconv.Itoa(ev.Col+1)...)
		b = append(b, ';')
		b = append(b, strconv.Itoa(ev.Row+1)...)
		return append(b, final)
	}
	if ev.Action == MouseRelease {
		// The default encoding does not say which button was released.
		cb = 3 | cb&^3
	}
	if ev.Col+33 > 255 || ev.Row+33 > 255 {
		return nil
	}
	return []byte{0x1b, '[', 'M', byte(cb + 32), byte(ev.Col + 33), byte(ev.Row + 33)}
}

// EncodeFocus is what a program that asked for focus events (1004)
// reads when the terminal gains or loses the focus, or nil.
func EncodeFocus(m Modes, focused bool) []byte {
	if !m.FocusReport {
		return nil
	}
	if focused {
		return []byte("\x1b[I")
	}
	return []byte("\x1b[O")
}

// ResetInputModes turns off what a program asked of the terminal's input
// (mouse and focus reports, bracketed paste, application keys), for when
// it went away without undoing them: its shell ended. With leaveAlt it
// leaves the alternate screen as well, for a new shell.
func (t *Terminal) ResetInputModes(leaveAlt bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.gen++
	t.modes.Mouse, t.modes.MouseTracking, t.modes.MouseSGR, t.modes.FocusReport = MouseOff, false, false, false
	t.modes.BracketedPaste, t.modes.AppCursorKeys, t.modes.AppKeypad = false, false, false
	if leaveAlt && t.altActive {
		t.switchScreen(false, true)
	}
}
