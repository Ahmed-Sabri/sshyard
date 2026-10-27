package app

import (
	"errors"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/locales"
)

// localID is the host of local terminal panes: this computer, never
// saved. Its panes split, move and close as any others, but it has no
// files, AI, forwards or metrics: there is no SSH connection under them.
const localID = "local:"

func isLocal(id string) bool { return id == localID }

// errLocalTerminalOnly is opening anything but a terminal on this computer.
var errLocalTerminalOnly = errors.New("app: this computer opens terminals only")

// localHost is the host of local terminals, named in the app's language.
func (a *App) localHost() assets.Host {
	name := a.localName
	if name == "" {
		name = locales.In("en").LocalTerminal()
	}
	return assets.Host{ID: localID, Name: name}
}

// OpenLocalTerminal opens a shell of this computer in a new tab.
func (a *App) OpenLocalTerminal() (int, error) {
	return a.open(localID, layout.Terminal, openAt{}, nil)
}

// remoteOnly says a pane is on a server, where files and AI can open.
func remoteOnly(cn *conn) bool { return cn != nil && !isLocal(cn.host.ID) }
