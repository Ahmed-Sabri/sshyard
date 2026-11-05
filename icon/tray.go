package icon

import (
	_ "embed"
	"runtime"
)

// Tray artwork is generated from SSHYard's tray-white.svg and logo-ui.png
// by traygen.go, independently of the application icons.
//
//go:embed tray-white.png
var trayWhite []byte

//go:embed tray-black.png
var trayBlack []byte

//go:embed tray-color.png
var trayColor []byte

// TrayPNG returns native tray artwork and whether the OS should tint it.
// The default is the rounded color logo. Monochrome artwork is white;
// Windows uses black on a light taskbar, while macOS uses the template's
// alpha for automatic contrast.
func TrayPNG(style string, dark bool) ([]byte, bool) {
	if style != "mono" {
		return trayColor, false
	}
	if runtime.GOOS == "windows" && !dark {
		return trayBlack, true
	}
	return trayWhite, true
}
