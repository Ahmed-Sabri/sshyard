package app

import (
	"strings"
	"testing"

	"github.com/Licoy/sshyard/locales"
)

// The questions before opening a large file or a program show its name
// as the skip notice does: quoted, on one line, nothing reordering it.
func TestOpenQuestionsEscapeName(t *testing.T) {
	a, tt, fc, _, _ := hostilePane(t, skipFS())
	for _, tc := range []struct {
		name, path string
		local      bool
		want       string
	}{
		{"large", "/d/big\n\u202etxt.exe.log", false, "“big\\n\\u202etxt.exe.log” is " + humanSize(2<<30) + "."},
		{"program", "/d/report\u202efdp.exe", true, "“report\\u202efdp.exe” is a type of file"},
		{"program, newline", "/d/a\r\nb\x1b.bat", true, "“a\\r\\nb\\x1b.bat” is a type of file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fc.fs.open, fc.fs.openOpen, fc.fs.with, fc.fs.withOpen = nil, false, nil, false
			if tc.local {
				a.openWith(fc, true, tc.path, 1, false, locales.In("en"))
			} else {
				a.editFile(fc, true, tc.path, 2<<30, false)
			}
			tt.Frame()
			found := false
			for _, s := range tt.Texts() {
				if strings.Contains(s, tc.want) {
					found = true
				}
				if strings.ContainsAny(s, "\n\r\x1b\u202e") {
					t.Errorf("shown as is: %q", s)
				}
			}
			if !found {
				t.Errorf("no %q in %q", tc.want, tt.Texts())
			}
			mustClick(t, tt, "Cancel")
			tt.Frame()
		})
	}
}
