//go:build !linux

package app

// desktopScale is 0 and 1: macOS and Windows give windows the desktop's
// scale, fractional ones too.
func desktopScale() (scale, text float32) { return 0, 1 }
