package sftpsvc

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
)

// ErrUnsafeName is a name from the server that is not safe as a local
// file name.
var ErrUnsafeName = errors.New("not a safe local file name")

// SafeName checks a name the server sent before it becomes a local file
// name, the same way on every OS (a listing made safe on one OS stays
// safe on another): it must be a single name, not "." or "..", without
// "/", "\", ":", NUL or other control characters, without a trailing
// dot or space (Windows drops them), and not a Windows device name (CON,
// PRN, AUX, NUL, COM1-9, LPT1-9, with or without an extension, in any
// case).
func SafeName(name string) error {
	bad := func(why string) error { return fmt.Errorf("%q: %w (%s)", name, ErrUnsafeName, why) }
	switch {
	case name == "":
		return bad("empty")
	case name == "." || name == "..":
		return bad("a relative folder")
	case strings.ContainsAny(name, `/\:`):
		return bad(`contains "/", "\" or ":"`)
	case strings.HasSuffix(name, ".") || strings.HasSuffix(name, " "):
		return bad("ends with a dot or space")
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return bad("contains a control character")
		}
	}
	if reservedName(name) {
		return bad("a Windows device name")
	}
	return nil
}

// reservedName says name is a Windows device name: what comes before the
// first dot, without trailing spaces, is one, whatever the case.
func reservedName(name string) bool {
	stem, _, _ := strings.Cut(name, ".")
	stem = strings.ToUpper(strings.TrimRight(stem, " ."))
	switch stem {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	if len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) {
		c := stem[3]
		return c >= '1' && c <= '9'
	}
	// COM¹ and the like count too on Windows.
	if strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT") {
		switch stem[3:] {
		case "¹", "²", "³":
			return true
		}
	}
	return false
}

// LocalPath joins rel, slash-separated names from the server, below the
// local folder dir: each name must pass SafeName and the result must stay
// inside dir.
func LocalPath(dir, rel string) (string, error) {
	for _, n := range strings.Split(rel, "/") {
		if err := SafeName(n); err != nil {
			return "", err
		}
	}
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if !Within(dir, p) {
		return "", fmt.Errorf("%q: %w (outside %s)", rel, ErrUnsafeName, dir)
	}
	return p, nil
}

// Within says the local path p is dir or inside it.
func Within(dir, p string) bool {
	r, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(p))
	if err != nil || filepath.IsAbs(r) {
		return false
	}
	return r == "." || (r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)))
}

// execExts are file types that opening in their default app may run.
var execExts = map[string]bool{
	"exe": true, "bat": true, "cmd": true, "com": true, "lnk": true, "ps1": true, "psm1": true,
	"vbs": true, "vbe": true, "js": true, "jse": true, "wsf": true, "wsh": true, "scr": true,
	"msi": true, "msp": true, "pif": true, "cpl": true, "hta": true, "reg": true, "inf": true,
	"scf": true, "url": true, "appref-ms": true, "jar": true, "app": true, "command": true,
	"tool": true, "pkg": true, "dmg": true, "sh": true, "bash": true, "zsh": true, "csh": true,
	"ksh": true, "run": true, "bin": true, "desktop": true, "appimage": true, "py": true,
	"pyw": true, "pl": true, "rb": true, "terminal": true, "workflow": true, "scpt": true,
	"applescript": true, "dll": true, "sys": true, "drv": true, "ocx": true, "gadget": true,
	"msc": true, "settingcontent-ms": true, "library-ms": true, "website": true,
	// Help, installers, launchers and disk images that open or mount as
	// code; Office files with macros; shortcuts that open a link.
	"chm": true, "msix": true, "appx": true, "appinstaller": true, "application": true,
	"jnlp": true, "iso": true, "img": true, "vhd": true, "vhdx": true, "webloc": true,
	"fileloc": true, "inetloc": true, "docm": true, "xlsm": true, "pptm": true, "ws": true,
	"psd1": true, "mst": true, "xll": true, "diagcab": true,
}

// Executable says opening a file so named in its default app may run
// it, by its extension (in any case; trailing dots and spaces dropped as
// Windows does).
func Executable(name string) bool {
	name = strings.TrimRight(name, ". ")
	i := strings.LastIndexByte(name, '.')
	if i < 0 {
		return false
	}
	return execExts[strings.ToLower(name[i+1:])]
}

// LocalMode is the mode a downloaded file gets for the server's mode m:
// read and write for the user, read for group and others where the
// server gives it (0644 or 0600 style). Execute bits are dropped: a
// file from a server is not made a program here, and neither are
// group or other write and set-id bits kept.
func LocalMode(m fs.FileMode) fs.FileMode {
	return m.Perm()&0o644 | 0o600
}
