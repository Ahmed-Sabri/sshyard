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
	label := string(cn.s.Term.OutputEncoding())
	a.menuChip(c, key, chip{
		Icon: icons.FileCode, Label: label, Tip: m.OutputEncodingTip(label), CompactMenu: true, FitMenu: true,
	}, func() []menuEntry { return a.outputEncodingEntries(c, cn, m) })
}

func (a *App) clearOutputEncodingMenu(currentKey string) {
	if strings.HasPrefix(a.menu.key, "output-encoding:") && a.menu.key != currentKey {
		a.menu = dropMenu{}
	}
}

func (a *App) outputEncodingEntries(c *ui.Context, cn *conn, m locales.Messages) []menuEntry {
	encodings := vt.OutputEncodings()
	entries := make([]menuEntry, 0, len(encodings)+7)
	for _, encoding := range encodings {
		switch encoding {
		case vt.EncodingGB2312, vt.EncodingBig5, vt.EncodingShiftJIS, vt.EncodingEUCKR,
			vt.EncodingWindows1252, vt.EncodingWindows1251, vt.EncodingCP437:
			entries = append(entries, menuSep)
		}
		note := ""
		if encoding == vt.EncodingGB2312 {
			note = m.OutputEncodingGB2312Note()
		}
		entries = append(entries, menuEntry{
			Label:   outputEncodingLabel(m, encoding),
			Note:    note,
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

func outputEncodingLabel(m locales.Messages, encoding vt.OutputEncoding) string {
	switch encoding {
	case vt.EncodingUTF8:
		return m.OutputEncodingUTF8()
	case vt.EncodingGB2312:
		return m.OutputEncodingGB2312()
	case vt.EncodingGBK:
		return m.OutputEncodingGBKLabel()
	case vt.EncodingGB18030:
		return m.OutputEncodingGB18030()
	case vt.EncodingBig5:
		return m.OutputEncodingBig5()
	case vt.EncodingShiftJIS:
		return m.OutputEncodingShiftJIS()
	case vt.EncodingEUCJP:
		return m.OutputEncodingEUCJP()
	case vt.EncodingEUCKR:
		return m.OutputEncodingEUCKR()
	case vt.EncodingWindows1252:
		return m.OutputEncodingWindows1252()
	case vt.EncodingWindows1250:
		return m.OutputEncodingWindows1250()
	case vt.EncodingISO8859_1:
		return m.OutputEncodingISO88591()
	case vt.EncodingISO8859_2:
		return m.OutputEncodingISO88592()
	case vt.EncodingISO8859_15:
		return m.OutputEncodingISO885915()
	case vt.EncodingWindows1251:
		return m.OutputEncodingWindows1251()
	case vt.EncodingKOI8R:
		return m.OutputEncodingKOI8R()
	case vt.EncodingKOI8U:
		return m.OutputEncodingKOI8U()
	case vt.EncodingISO8859_5:
		return m.OutputEncodingISO88595()
	case vt.EncodingCP437:
		return m.OutputEncodingCP437()
	case vt.EncodingCP850:
		return m.OutputEncodingCP850()
	case vt.EncodingCP866:
		return m.OutputEncodingCP866()
	default:
		panic("unknown output encoding: " + string(encoding))
	}
}
