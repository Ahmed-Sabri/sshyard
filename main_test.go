package main

import (
	"testing"

	"github.com/Licoy/sshyard/internal/theme"
	"github.com/egoist/mygo"
)

func TestThemeSource(t *testing.T) {
	for m, want := range map[theme.Mode]mygo.ThemeSource{theme.Auto: mygo.ThemeSystem, theme.Light: mygo.ThemeLight, theme.Dark: mygo.ThemeDark} {
		if got := themeSource(m); got != want {
			t.Errorf("themeSource(%q) = %q, want %q", m, got, want)
		}
	}
}
