// SSHYard is a cross-platform SSH client with a native interface: hosts,
// tabs and split panes of terminals drawn by its own VT engine, and SFTP.
package main

import (
	"log"
	"os"
	"runtime"

	"github.com/Licoy/sshyard/internal/app"
	"github.com/Licoy/sshyard/internal/gtkfix"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/internal/updater"
	_ "github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func themeSource(m theme.Mode) mygo.ThemeSource {
	switch m {
	case theme.Light:
		return mygo.ThemeLight
	case theme.Dark:
		return mygo.ThemeDark
	}
	return mygo.ThemeSystem
}

// platform is the app's hooks into MyGo.
func platform() app.Platform {
	dir := os.Getenv("SSHYARD_DATA_DIR")
	if dir == "" {
		var err error
		if dir, err = mygo.App.Path(mygo.PathUserData); err != nil {
			log.Printf("sshyard: no data directory, keeping data in memory: %v", err)
		}
	}
	return app.Platform{
		DataDir:   dir,
		Version:   mygo.App.Version(),
		Secrets:   keychain.System{},
		Updates:   updater.MygoBackend{},
		SetTheme:  func(m theme.Mode) { mygo.Theme.SetSource(themeSource(m)) },
		SetLocale: mygo.App.SetLocale,
		OpenPath:  mygo.Shell.OpenPath,
		PickFile: func(title string) (string, error) {
			paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: title, ShowHiddenFiles: true})
			if err != nil || len(paths) == 0 {
				return "", err
			}
			return paths[0], nil
		},
		SaveFile: func(title, path string) (string, error) {
			return mygo.Dialog.Save(mygo.SaveDialogOptions{Title: title, DefaultPath: path, CreateDirectories: true})
		},
	}
}

// windowVibrancy is the material behind the window: the sidebar material
// on macOS only (on Windows a material window loses its menu bar, and
// Linux has none).
func windowVibrancy() mygo.Vibrancy {
	if runtime.GOOS == "darwin" {
		return mygo.VibrancySidebar
	}
	return mygo.VibrancyNone
}

// ownWindowButtons says the app draws the window's buttons in its title
// bar: on Linux and Windows. macOS keeps its traffic lights.
func ownWindowButtons() bool { return runtime.GOOS != "darwin" }

func main() {
	mygo.App.WhenReady(func() {
		// Linux: size the window buttons' bar to its buttons (see gtkfix).
		gtkfix.Apply()
		a, err := app.New(platform())
		if err != nil {
			log.Printf("sshyard: %v", err)
		}
		a.Start()
		mygo.App.OnQuit(a.Close)
		w := mygo.NewWindow(mygo.WindowOptions{
			Title:     "SSHYard",
			Width:     1200,
			Height:    760,
			MinWidth:  820,
			MinHeight: 520,
			StateKey:  "main",
			// One unified toolbar: the page draws the title area, the
			// window controls sit over it, inset on macOS.
			TitleBarStyle:        mygo.TitleBarHiddenInset,
			TitleBarHeight:       40,
			TrafficLightPosition: &mygo.Point{X: 14, Y: 13},
			// Linux and Windows: the title bar draws the window's
			// buttons, in the app's look (app.WindowControls).
			HideWindowButtons: ownWindowButtons(),
			// The sidebar's glass: the system's material on macOS, where
			// the sidebar leaves its fill clear; a solid fill elsewhere.
			Vibrancy:        windowVibrancy(),
			BackgroundColor: "light-dark(#EDEDF0,#28282A)",
			// Native UI only: no web view anywhere in SSHYard.
			Content: ui.View(a.View),
		})
		w.OnMinimize(func() { a.SetMinimized(true) })
		w.OnRestore(func() { a.SetMinimized(false) })
		if ownWindowButtons() {
			// Off the frame that clicked them: closing the window there
			// would take it down under the view.
			a.SetWindow(app.WindowControls{
				Minimize:   func() { go w.Minimize() },
				Maximize:   func() { go w.Maximize() },
				Unmaximize: func() { go w.Unmaximize() },
				Close:      func() { go w.Close() },
			})
			a.SetMaximized(w.IsMaximized())
			w.OnMaximize(func() { a.SetMaximized(true) })
			w.OnUnmaximize(func() { a.SetMaximized(false) })
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
