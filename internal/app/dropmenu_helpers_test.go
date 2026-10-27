package app

import (
	"errors"
	"fmt"
	"slices"
)

// menuOf is the context menu shown in a, its labels ("-" for a
// separator), or none.
func menuOf(a *App) []string {
	if a.menu.key != ctxMenuKey || a.ctx.entries == nil {
		return nil
	}
	return menuLabels(a.ctx.entries())
}

// chooseMenu chooses the entry at path (through submenus) of the context
// menu shown in a, as clicking it does, then draws a frame.
func chooseMenu(a *App, tt interface{ Frame() }, path ...string) error {
	if a.menu.key != ctxMenuKey || a.ctx.entries == nil {
		return errors.New("no context menu is shown")
	}
	es := a.ctx.entries()
	for i, label := range path {
		j := slices.IndexFunc(es, func(e menuEntry) bool { return !e.Sep && e.Label == label })
		if j < 0 {
			return fmt.Errorf("no %q in menu %q", label, menuLabels(es))
		}
		e := es[j]
		if e.Disabled {
			return fmt.Errorf("%q is disabled", label)
		}
		if i < len(path)-1 {
			es = e.Sub
			continue
		}
		a.menu, a.ctx.entries = dropMenu{}, nil
		if e.Do != nil {
			e.Do()
		}
	}
	tt.Frame()
	return nil
}
