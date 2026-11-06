//go:build !linux

package gtkfix

// Apply does nothing: only GTK's header bar needs the fix.
func Apply() bool { return false }

// TextColor does nothing: only cairo's glyphs on Linux need the fix.
func TextColor() bool { return false }
