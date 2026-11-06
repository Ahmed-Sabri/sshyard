//go:build !darwin

package macfix

// TitleBarClicks does nothing: only AppKit's title bar needs the fix.
func TitleBarClicks() bool { return false }
