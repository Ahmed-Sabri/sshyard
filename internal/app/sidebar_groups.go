package app

import (
	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// Separate value types keep hosts moving into groups and groups reordering.
type dragGroup struct{ Name string }
type sidebarDrop interface{ sidebarDrop() }

func (dragHost) sidebarDrop()  {}
func (dragGroup) sidebarDrop() {}

func (a *App) groupDragDrop(c *ui.Context, m locales.Messages, tok theme.Tokens, sec ui.Element, g string) {
	if a.search == "" {
		sec.Drag(dragGroup{g})
	}
	_, y, _ := sec.PointerPosition()
	after := y >= sec.Bounds().H/2
	if v, ok := ui.DragOver[sidebarDrop](sec); ok {
		switch d := v.(type) {
		case dragGroup:
			if a.search == "" && d.Name != g {
				sec.DrawOver(func(p *ui.Painter, r ui.Rect) {
					if after {
						r.Y += r.H - 2
					}
					r.H = 2
					p.Fill(r, tok.Accent, 1)
				})
			}
		case dragHost:
			if h, ok := a.store.Get(d.ID); ok && h.Group != g {
				sec.Background(tok.Accent.Alpha(0.08))
			}
		}
	}
	if v, ok := ui.Drop[sidebarDrop](sec); ok {
		var err error
		switch d := v.(type) {
		case dragGroup:
			if a.search == "" {
				err = a.store.MoveGroup(d.Name, g, after)
			}
		case dragHost:
			if h, ok := a.store.Get(d.ID); ok && h.Group != g {
				err = a.store.SetGroup(d.ID, g)
			}
		}
		if err != nil {
			c.AddToast(ui.Toast{Title: m.ErrGeneric(err.Error()), Type: "error"})
		}
	}
}

// Include sections hidden by search, but ignore stale state of deleted groups.
func (a *App) sidebarSectionKeys() []string {
	var keys []string
	for _, g := range a.store.Groups() {
		keys = append(keys, "g:"+g)
	}
	if len(a.store.Favorites()) > 0 {
		keys = append(keys, "fav")
	}
	if len(a.store.InGroup("")) > 0 {
		keys = append(keys, "none")
	}
	if len(a.store.Forwards()) > 0 {
		keys = append(keys, "fwd")
	}
	return keys
}

func (a *App) sidebarSectionsButton(c *ui.Context, m locales.Messages) {
	keys := a.sidebarSectionKeys()
	anyOpen := false
	for _, key := range keys {
		anyOpen = anyOpen || *a.section(key)
	}
	icon, label := icons.Collapse, m.ExpandAllGroups()
	if anyOpen {
		icon, label = icons.Expand, m.CollapseAllGroups()
	}
	if (IconButton{Icon: icon, Label: label, Size: Small, Disabled: len(keys) == 0}).Clicked(c, "sections") {
		for _, key := range keys {
			*a.section(key) = !anyOpen
		}
	}
}
