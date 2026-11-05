package app

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"time"
)

// desktopScale is the scale the X11 desktop asks for, Xft.dpi as KDE and
// others set it for 150%, which GTK's whole scale misses, and GTK's text
// scale (GDK_DPI_SCALE). Wayland's compositor scales a window itself.
func desktopScale() (scale, text float32) {
	text = 1
	if v, err := strconv.ParseFloat(os.Getenv("GDK_DPI_SCALE"), 32); err == nil && v >= 0.25 && v <= 4 {
		text = float32(v)
	}
	if os.Getenv("DISPLAY") == "" || os.Getenv("WAYLAND_DISPLAY") != "" {
		return 0, text
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "xrdb", "-query").Output()
	if err != nil {
		return 0, text
	}
	return parseXftDPI(string(out)), text
}
