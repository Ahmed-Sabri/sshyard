package session

import (
	"slices"
	"testing"
)

// TestMergeEnv: the last entry of a name wins, in the place the name
// first had; names fold case on Windows only, and Windows' per-drive
// directories, named from "=", stay apart.
func TestMergeEnv(t *testing.T) {
	env := []string{"POWERSHELL_UPDATECHECK=Off", "=C:=C:\\Users", "=D:=D:\\", "Path=C:\\bin", "powershell_updatecheck=LTS", "TERM=dumb", "PATH=C:\\tools", "TERM=xterm-256color", "EMPTY"}
	got := mergeEnv(env, true)
	want := []string{"powershell_updatecheck=LTS", "=C:=C:\\Users", "=D:=D:\\", "PATH=C:\\tools", "TERM=xterm-256color", "EMPTY"}
	if !slices.Equal(got, want) {
		t.Fatalf("folded: %q, want %q", got, want)
	}
	got = mergeEnv([]string{"Path=a", "PATH=b", "Path=c"}, false)
	if want := []string{"Path=c", "PATH=b"}; !slices.Equal(got, want) {
		t.Fatalf("case-sensitive: %q, want %q", got, want)
	}
}
