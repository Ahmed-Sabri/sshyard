// Package fonts lists the font families installed on the machine, for the
// interface's and the terminal's font settings. It asks MyGo's text engine
// first (ui.FontFamilies: Core Text, DirectWrite, fontconfig through
// Pango), which knows what text can actually draw with; where that lists
// nothing, it reads the system itself, without cgo: fontconfig's fc-list
// on Linux when present, else the font files in the platform's font
// directories (macOS's, %WINDIR%\Fonts and the per-user fonts on Windows,
// fontconfig's on Linux), whose names and pitch it parses from their
// OpenType tables.
package fonts

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// Family is an installed font family.
type Family struct {
	Name string
	// Mono is whether it is monospaced, as a terminal wants.
	Mono bool
}

// engine is ui.FontFamilies, replaced in tests.
var engine = func() []Family {
	var out []Family
	for _, f := range ui.FontFamilies() {
		out = append(out, Family{Name: f.Name, Mono: f.Monospaced})
	}
	return out
}

// List lists the installed families, each once, sorted without regard to
// case.
func List() []Family {
	if fs := engine(); len(fs) > 0 {
		return Normalize(fs)
	}
	return Normalize(Scan())
}

// Scan reads the system's families without the text engine.
func Scan() []Family {
	if runtime.GOOS == "linux" || runtime.GOOS == "freebsd" {
		if fs := fcList(); len(fs) > 0 {
			return fs
		}
	}
	home, _ := os.UserHomeDir()
	return ScanDirs(Dirs(runtime.GOOS, os.Getenv, home))
}

func fcList() []Family {
	path, err := exec.LookPath("fc-list")
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--format", "%{family}\t%{spacing}\n").Output()
	if err != nil {
		return nil
	}
	return ParseFCList(string(out))
}

// ParseFCList parses `fc-list --format '%{family}\t%{spacing}\n'`: per
// font, its family names (a comma-separated list, the first the
// canonical one; commas within names are escaped as "\,") and its
// spacing, 100 for monospaced and 90 for dual-width (as CJK monospace
// fonts are), empty for proportional.
func ParseFCList(out string) []Family {
	var fs []Family
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		names, spacing, _ := strings.Cut(line, "\t")
		name := firstFamily(names)
		if name == "" {
			continue
		}
		sp := strings.TrimSpace(spacing)
		fs = append(fs, Family{Name: name, Mono: sp == "100" || sp == "90" || sp == "110" || sp == "mono" || sp == "dual" || sp == "charcell"})
	}
	return fs
}

func firstFamily(list string) string {
	var b strings.Builder
	for i := 0; i < len(list); i++ {
		switch {
		case list[i] == '\\' && i+1 < len(list):
			i++
			b.WriteByte(list[i])
		case list[i] == ',':
			return strings.TrimSpace(b.String())
		default:
			b.WriteByte(list[i])
		}
	}
	return strings.TrimSpace(b.String())
}

// Dirs are where a platform keeps its fonts, the user's own included.
func Dirs(goos string, getenv func(string) string, home string) []string {
	var ds []string
	switch goos {
	case "darwin":
		ds = []string{"/System/Library/Fonts", "/Library/Fonts", "/Network/Library/Fonts"}
		if home != "" {
			ds = append(ds, filepath.Join(home, "Library", "Fonts"))
		}
	case "windows":
		win := getenv("WINDIR")
		if win == "" {
			win = getenv("SystemRoot")
		}
		if win == "" {
			win = `C:\Windows`
		}
		ds = []string{win + `\Fonts`}
		if l := getenv("LOCALAPPDATA"); l != "" {
			// fonts installed for the user alone, as Settings does
			ds = append(ds, l+`\Microsoft\Windows\Fonts`)
		}
	default:
		if x := getenv("XDG_DATA_HOME"); x != "" {
			ds = append(ds, filepath.Join(x, "fonts"))
		} else if home != "" {
			ds = append(ds, filepath.Join(home, ".local", "share", "fonts"))
		}
		if home != "" {
			ds = append(ds, filepath.Join(home, ".fonts"))
		}
		dataDirs := getenv("XDG_DATA_DIRS")
		if dataDirs == "" {
			dataDirs = "/usr/local/share:/usr/share"
		}
		for _, d := range strings.Split(dataDirs, ":") {
			if d != "" {
				ds = append(ds, filepath.Join(d, "fonts"))
			}
		}
	}
	return ds
}

// ScanDirs reads the families of the font files under dirs.
func ScanDirs(dirs []string) []Family {
	var fs []Family
	for _, d := range dirs {
		filepath.WalkDir(d, func(path string, e os.DirEntry, err error) error {
			if err != nil || e.IsDir() {
				return nil
			}
			switch strings.ToLower(filepath.Ext(path)) {
			case ".ttf", ".otf", ".ttc", ".otc":
			default:
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			if got, err := ParseFont(data); err == nil {
				fs = append(fs, got...)
			}
			return nil
		})
	}
	return fs
}

// Normalize drops empty and hidden names (".SF NS", as macOS keeps for
// itself) and lists each family once, monospaced if any of its fonts
// is, sorted without regard to case.
func Normalize(fs []Family) []Family {
	idx := map[string]int{}
	var out []Family
	for _, f := range fs {
		f.Name = strings.TrimSpace(f.Name)
		if f.Name == "" || strings.HasPrefix(f.Name, ".") {
			continue
		}
		k := strings.ToLower(f.Name)
		if i, ok := idx[k]; ok {
			out[i].Mono = out[i].Mono || f.Mono
			continue
		}
		idx[k] = len(out)
		out = append(out, f)
	}
	slices.SortStableFunc(out, func(a, b Family) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return out
}

// ForTerminal orders families for a terminal's font setting: the
// monospaced ones first, then, unless monoOnly, the others.
func ForTerminal(fs []Family, monoOnly bool) []Family {
	var mono, rest []Family
	for _, f := range fs {
		if f.Mono {
			mono = append(mono, f)
		} else if !monoOnly {
			rest = append(rest, f)
		}
	}
	return append(mono, rest...)
}

// Has reports whether name is one of fs, without regard to case.
func Has(fs []Family, name string) bool {
	for _, f := range fs {
		if strings.EqualFold(f.Name, name) {
			return true
		}
	}
	return false
}
