// Package icons is SSHYard's icon set: SVGs from Remix Icon
// (https://remixicon.com, github.com/Remix-Design/RemixIcon, v4.8.0,
// Apache License 2.0; see LICENSE and NOTICE in this directory), embedded
// in the binary and drawn natively by MyGo's ui (ui.Icon), in the color of
// the text around them. No font, no webview.
//
// Every icon of the app comes from here. The "-line" style is the default;
// a "-fill" variant is used only for an active state (the settings button
// while the settings show, a favorite host, the active tab's kind). To add
// one, copy icons/<Category>/<name>.svg from the Remix Icon v4.8.0 tag into
// svg/ unchanged, and add a variable below with its file name.
package icons

import (
	"embed"
	"io/fs"
	"strings"

	"github.com/egoist/mygo/ui"
)

//go:embed svg/*.svg
var files embed.FS

// Icon is one parsed Remix Icon.
type Icon struct {
	Name string // the Remix Icon name, as "folder-3-line"
	SVG  *ui.SVG
}

var all = map[string]*Icon{}

func load(name string) *Icon {
	data, err := files.ReadFile("svg/" + name + ".svg")
	if err != nil {
		panic("icons: no Remix Icon " + name + " in svg/")
	}
	ic := &Icon{Name: name, SVG: ui.MustParseSVG(data)}
	all[name] = ic
	return ic
}

// The icons the app uses, by what they mean.
var (
	Host       = load("server-line")
	Add        = load("add-line")
	Settings   = load("settings-3-line")
	SettingsOn = load("settings-3-fill")
	Star       = load("star-line")
	StarOn     = load("star-fill")
	Folder     = load("folder-3-line")
	FolderOn   = load("folder-3-fill")
	File       = load("file-3-line")
	SplitRight = load("layout-column-line")
	SplitDown  = load("layout-row-line")
	Close      = load("close-line")
	Refresh    = load("refresh-line")
	Upload     = load("upload-2-line")
	Download   = load("download-2-line")
	GoUp       = load("corner-left-up-line")
	Terminal   = load("terminal-box-line")
	TerminalOn = load("terminal-box-fill")
	Edit       = load("edit-line")
	Delete     = load("delete-bin-line")
	Search     = load("search-line")
	Mouse      = load("mouse-line")
	NewFolder  = load("folder-add-line")
	Reconnect  = load("restart-line")
	Clear      = load("eraser-line")
	ClearText  = load("close-circle-line")
	LocalDisk  = load("computer-line")
	Warning    = load("error-warning-line")
	Appearance = load("palette-line")
	Key        = load("key-2-line")
	Password   = load("lock-password-line")
	Generate   = load("magic-line")
	OpenFile   = load("folder-open-line")
	Paste      = load("clipboard-line")
	InstallKey = load("shield-check-line")
	Proxy      = load("global-line")
	JumpHost   = load("route-line")
	Quick      = load("flashlight-line")
	Save       = load("save-line")
	Import     = load("import-line")
	Forward    = load("arrow-left-right-line")
	Run        = load("play-circle-line")
	RunOn      = load("play-circle-fill")
	Disconnect = load("shut-down-line")
	Pause      = load("pause-line")
	Resume     = load("play-line")
	NewFile    = load("file-add-line")
	EditFile   = load("file-edit-line")
	EditFileOn = load("file-edit-fill")
	OpenWith   = load("external-link-line")
	CopyPath   = load("file-copy-line")
	Chmod      = load("lock-line")
	Follow     = load("footprint-line")
	FollowOn   = load("footprint-fill")
	Collapse   = load("arrow-down-s-line")
	Expand     = load("arrow-up-s-line")
	Rename     = load("pencil-line")
	Robot      = load("robot-2-line")
	RobotOn    = load("robot-2-fill")
	Stop       = load("stop-circle-line")
)

// Get returns an icon by its Remix Icon name.
func Get(name string) (*Icon, bool) {
	ic, ok := all[name]
	return ic, ok
}

// Names lists the embedded icon files' names.
func Names() []string {
	var out []string
	es, _ := fs.ReadDir(files, "svg")
	for _, e := range es {
		out = append(out, strings.TrimSuffix(e.Name(), ".svg"))
	}
	return out
}
