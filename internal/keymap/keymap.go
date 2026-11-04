// Package keymap is a keyboard shortcut as SSHYard's settings keep it:
// its modifiers and key, written "super+shift+p".
package keymap

import (
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
)

// Combo is a shortcut: modifiers held and a key pressed.
type Combo struct {
	Mods ui.Modifiers
	Key  ui.Key
}

// mods are the modifiers in the order they are written.
var mods = []struct {
	mod  ui.Modifiers
	name string
}{{ui.Ctrl, "ctrl"}, {ui.Alt, "alt"}, {ui.Shift, "shift"}, {ui.Super, "super"}}

// names are the keys a shortcut may use: letters, digits, punctuation
// and the function keys. The others edit text or move in it.
var names = func() map[ui.Key]string {
	n := map[ui.Key]string{
		ui.KeyMinus: "minus", ui.KeyEqual: "equal", ui.KeyComma: "comma", ui.KeyPeriod: "period",
		ui.KeySlash: "slash", ui.KeySemicolon: "semicolon", ui.KeyQuote: "quote",
		ui.KeyBracketLeft: "bracketleft", ui.KeyBracketRight: "bracketright",
		ui.KeyBackslash: "backslash", ui.KeyBackquote: "backquote",
	}
	for k := ui.KeyA; k <= ui.KeyZ; k++ {
		n[k] = string(rune('a' + k - ui.KeyA))
	}
	for k := ui.Key0; k <= ui.Key9; k++ {
		n[k] = string(rune('0' + k - ui.Key0))
	}
	for k := ui.KeyF1; k <= ui.KeyF12; k++ {
		n[k] = "f" + strconv.Itoa(int(k-ui.KeyF1)+1)
	}
	return n
}()

// Bindable reports a key a shortcut may use.
func Bindable(k ui.Key) bool {
	_, ok := names[k]
	return ok
}

// String writes c as the settings keep it, "ctrl+shift+p".
func (c Combo) String() string {
	var parts []string
	for _, m := range mods {
		if c.Mods&m.mod != 0 {
			parts = append(parts, m.name)
		}
	}
	return strings.Join(append(parts, names[c.Key]), "+")
}

// Parse reads what String writes.
func Parse(s string) (Combo, bool) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(s)), "+")
	var c Combo
	for _, p := range parts[:len(parts)-1] {
		found := false
		for _, m := range mods {
			if p == m.name && c.Mods&m.mod == 0 {
				c.Mods, found = c.Mods|m.mod, true
			}
		}
		if !found {
			return Combo{}, false
		}
	}
	last := parts[len(parts)-1]
	for k, n := range names {
		if n == last {
			c.Key = k
			return c, true
		}
	}
	return Combo{}, false
}
