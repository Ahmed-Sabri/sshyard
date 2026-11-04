package app

import (
	"github.com/Licoy/sshyard/internal/fileicons"
	"github.com/Licoy/sshyard/internal/sftpsvc"
	"github.com/egoist/mygo/ui"
)

func entryIcon(e sftpsvc.Entry) *fileicons.Icon {
	return fileicons.Lookup(e.Name, e.IsDir)
}

// fileImage preserves the artwork's colors, including in selected rows.
// The adjacent file name supplies the accessible text.
func fileImage(c *ui.Context, icon *fileicons.Icon, size float32) {
	src := icon.Light
	if currentTokens(c).Dark {
		src = icon.Dark
	}
	ui.Image(c, src).Size(size, size).Shrink(0)
}

func fileMenuEntry(name string, isDir bool) menuEntry {
	icon := fileicons.Lookup(name, isDir)
	return menuEntry{Image: icon.Light, DarkImage: icon.Dark, Label: shownName(name)}
}
