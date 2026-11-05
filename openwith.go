package main

import (
	"os/exec"
	"runtime"

	"github.com/Licoy/sshyard/internal/app"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// openWith lets the user choose the app that opens path: the system's
// own chooser on Windows, an app picked in Applications on macOS, a
// program in /usr/bin on Linux.
func openWith(path, title string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32.exe", "shell32.dll,OpenAs_RunDLL", path)
	case "darwin":
		apps, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: title, DefaultPath: "/Applications", Filters: []mygo.FileFilter{{Name: title, Extensions: []string{"app"}}}})
		if err != nil || len(apps) == 0 {
			return err
		}
		cmd = exec.Command("open", "-a", apps[0], path)
	default:
		progs, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: title, DefaultPath: "/usr/bin"})
		if err != nil || len(progs) == 0 {
			return err
		}
		cmd = exec.Command(progs[0], path)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

// openWindow opens a window beside the main one, for the file editor.
func openWindow(s app.WindowSpec) app.Window {
	var w *mygo.Window
	mygo.RunOnMain(func() {
		prepareWindowState(s.StateKey)
		w = mygo.NewWindow(mygo.WindowOptions{
			Title:           s.Title,
			Width:           s.Width,
			Height:          s.Height,
			MinWidth:        420,
			MinHeight:       300,
			StateKey:        s.StateKey,
			BackgroundColor: "light-dark(#EDEDF0,#28282A)",
			Content:         ui.View(s.View),
		})
		rememberWindow(w, s.StateKey)
		w.OnClose(func(e *mygo.CloseEvent) {
			if s.Closing != nil && !s.Closing() {
				e.PreventDefault()
			}
		})
		w.OnClosed(func() {
			if s.Closed != nil {
				s.Closed()
			}
		})
	})
	return window{w}
}

// window is a mygo.Window as an app.Window.
type window struct{ w *mygo.Window }

func (w window) Focus() { w.w.Show() }

// Close closes the window without asking: the app already did.
func (w window) Close()                { w.w.Destroy() }
func (w window) SetTitle(title string) { w.w.SetTitle(title) }
func (w window) SetEdited(edited bool) { w.w.SetDocumentEdited(edited) }
