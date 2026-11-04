package keymap

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		c Combo
		s string
	}{
		{Combo{ui.Super | ui.Shift, ui.KeyP}, "shift+super+p"},
		{Combo{ui.Ctrl | ui.Shift, ui.KeyK}, "ctrl+shift+k"},
		{Combo{ui.Ctrl | ui.Alt | ui.Shift | ui.Super, ui.KeyPeriod}, "ctrl+alt+shift+super+period"},
		{Combo{ui.Super, ui.Key7}, "super+7"},
		{Combo{ui.Super, ui.KeyF1}, "super+f1"},
		{Combo{ui.Ctrl | ui.Shift, ui.KeyF12}, "ctrl+shift+f12"},
		{Combo{ui.Super, ui.KeyBracketLeft}, "super+bracketleft"},
	} {
		if got := tc.c.String(); got != tc.s {
			t.Errorf("%+v: %q, want %q", tc.c, got, tc.s)
		}
		if got, ok := Parse(tc.s); !ok || got != tc.c {
			t.Errorf("Parse(%q) = %+v %v", tc.s, got, ok)
		}
	}
	if c, ok := Parse(" Super+Shift+P "); !ok || c != (Combo{ui.Super | ui.Shift, ui.KeyP}) {
		t.Errorf("case and spaces: %+v %v", c, ok)
	}
	for _, s := range []string{"", "super", "super+", "super+enter", "super+up", "hyper+k", "super+super+k", "super+f13", "k+super"} {
		if c, ok := Parse(s); ok {
			t.Errorf("Parse(%q) = %+v", s, c)
		}
	}
	if Bindable(ui.KeyEnter) || Bindable(ui.KeyLeft) || !Bindable(ui.KeyF5) || !Bindable(ui.KeyBackquote) {
		t.Error("Bindable")
	}
}
