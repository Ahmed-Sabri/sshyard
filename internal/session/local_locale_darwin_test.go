package session

import (
	"strings"
	"testing"
)

func TestLocalUTF8LocaleInput(t *testing.T) {
	clearLocalTestLocale(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ZDOTDIR", home)
	// -f skips system/user profiles that could supply a locale and hide the bug.
	sh := Shell{Path: "/bin/zsh", Args: []string{"-f", "-l"}, Env: []string{
		"PS1=BOOT> ", "PS2=", "RPS1=", "PROMPT_EOL_MARK=",
	}}
	s := New(Config{}, Options{Local: true, Shell: sh}, 100, 24)
	defer s.Close()
	defer func() {
		if t.Failed() {
			t.Logf("terminal screen: %q", s.Term.ScreenText())
		}
	}()
	s.Start()
	waitState(t, s, Connected)
	write := func(input string) {
		t.Helper()
		if n, err := s.Write([]byte(input)); err != nil || n != len(input) {
			t.Fatalf("write input: %d/%d bytes, %v", n, len(input), err)
		}
	}
	waitText(t, s, "BOOT>")
	// Clear the setup command so its echoed prompt cannot satisfy the wait.
	write("unsetopt prompt_sp prompt_cr beep; unset zle_bracketed_paste; bindkey -e; PS1='LOCALE> '; printf '\\033[2J\\033[H'\r")
	waitFor(t, "isolated prompt", func() bool { return s.Term.ScreenText() == "LOCALE>" })
	write("echo 中文")
	waitText(t, s, "LOCALE> echo 中文") // Must display correctly before Enter.
	t.Logf("before Enter: %q", s.Term.ScreenText())
	write("\r")
	waitFor(t, "Chinese output and next prompt", func() bool {
		return strings.Contains(s.Term.ScreenText(), "LOCALE> echo 中文\n中文\nLOCALE>")
	})
	t.Logf("after Enter: %q", s.Term.ScreenText())
	write("exit\r")
	waitState(t, s, Exited)
}
