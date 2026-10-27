package gtkfix

import (
	"strings"
	"testing"
)

func TestCSSTargetsOnlyMyGoControls(t *testing.T) {
	for _, block := range strings.Split(CSS, "}") {
		rule, _, ok := strings.Cut(block, "{")
		if !ok {
			continue
		}
		for _, sel := range strings.Split(rule, ",") {
			if sel = strings.TrimSpace(sel); sel != "headerbar.mygo-window-controls" && !strings.HasPrefix(sel, "headerbar.mygo-window-controls ") {
				t.Errorf("selector %q reaches beyond the window buttons' bar", sel)
			}
		}
	}
}
