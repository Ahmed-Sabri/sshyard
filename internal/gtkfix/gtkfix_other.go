//go:build !linux

package gtkfix

// Apply does nothing: only GTK's header bar needs the fix.
func Apply() bool { return false }
