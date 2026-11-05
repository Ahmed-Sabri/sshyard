package app

import (
	"strconv"
	"strings"

	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/vt"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// Each pane owns its menu key and callbacks, so a later focus change cannot
// redirect a choice to another terminal.
func (a *App) outputEncodingChip(c *ui.Context, m locales.Messages) {
	cn := a.focused()
	key := ""
	if cn != nil && cn.kind == layout.Terminal {
		key = "output-encoding:" + strconv.Itoa(cn.id)
	}
	a.clearOutputEncodingMenu(key)
	if a.page != pageWorkspace || cn == nil || cn.kind != layout.Terminal || cn.s == nil || cn.s.Term == nil {
		return
	}
	label := m.OutputEncodingUTF8()
	if cn.s.Term.OutputEncoding() == vt.EncodingGBK {
		label = m.OutputEncodingGBK()
	}
	a.menuChip(c, key, chip{
		Icon: icons.FileCode, Label: label, Tip: m.OutputEncodingTip(label), CompactMenu: true,
	}, func() []menuEntry { return a.outputEncodingEntries(c, cn, m) })
}

func (a *App) clearOutputEncodingMenu(currentKey string) {
	if strings.HasPrefix(a.menu.key, "output-encoding:") && a.menu.key != currentKey {
		a.menu = dropMenu{}
	}
}

func (a *App) outputEncodingEntries(c *ui.Context, cn *conn, m locales.Messages) []menuEntry {
	entries := make([]menuEntry, 0, 2)
	for i, encoding := range []vt.OutputEncoding{vt.EncodingUTF8, vt.EncodingGBK} {
		entries = append(entries, menuEntry{
			Label:   []string{m.OutputEncodingUTF8(), m.OutputEncodingGBK()}[i],
			Checked: cn.s.Term.OutputEncoding() == encoding,
			Do: func() {
				if err := cn.s.Term.SetOutputEncoding(encoding); err != nil {
					c.AddToast(ui.Toast{Title: m.OutputEncodingError(err.Error()), Type: "error"})
				}
				a.focusReq = cn.id
			},
		})
	}
	return entries
}
