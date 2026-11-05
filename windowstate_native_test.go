package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func TestLayoutWindowStatesNative(t *testing.T) {
	if os.Getenv("SSHYARD_NATIVE_TEST") != "1" {
		t.Skip("set SSHYARD_NATIVE_TEST=1 on a desktop session for real geometry acceptance")
	}
	dir := t.TempDir()
	for _, mode := range []string{"layout-save", "layout-restore", "layout-disable", "layout-off", "layout-enable", "layout-current"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(ctx, exe, "-test.run=^$")
			cmd.Env = append(os.Environ(), "SSHYARD_NATIVE_HELPER="+mode, "SSHYARD_NATIVE_DIR="+dir)
			output, err := cmd.CombinedOutput()
			if err != nil || !bytes.Contains(output, []byte("native geometry passed: "+mode)) {
				t.Fatalf("native geometry subprocess: %v\n%s", err, output)
			}
			t.Log(string(output))
		})
	}
}

// Each mode runs in a new process so MyGo's own window-state cache cannot
// accidentally satisfy a restart assertion, and all data stays in t.TempDir.
func runNativeLayout(mode, dir string) {
	mygo.App.SetName("SSHYard Native Geometry Test")
	mygo.App.SetPath(mygo.PathUserData, dir)
	on := mode != "layout-off" && mode != "layout-enable"
	s := &layoutWindowStates{path: filepath.Join(dir, "window-state.json"), enabled: on, current: map[*mygo.Window]string{}, reportRead: func(err error) { panic(err) }, reportSave: func(err error) { panic(err) }}
	nativeLayout = s
	mygo.App.SetWindowStateStore(s)
	mygo.App.WhenReady(func() {
		prepareWindowState("main")
		w := mygo.NewWindow(mygo.WindowOptions{Title: "Geometry Test", Width: 820, Height: 520, StateKey: "main", Content: ui.View(func(c *ui.Context) { ui.Text(c, "Geometry acceptance") })})
		rememberWindow(w, "main")
		go func() {
			// Let native initial placement finish before comparing or resizing.
			time.Sleep(400 * time.Millisecond)
			mygo.RunOnMain(func() {
				b := w.Bounds()
				switch mode {
				case "layout-restore":
					nativeCheck(b.X == 120 && b.Y == 120 && b.Width == 960 && b.Height == 640, fmt.Sprintf("restart did not restore saved outer bounds: %+v", b))
				case "layout-current":
					nativeCheck(b.X == 140 && b.Y == 130 && b.Width == 1020 && b.Height == 680, fmt.Sprintf("restart did not restore reenabled current bounds: %+v", b))
				case "layout-off", "layout-enable":
					nativeCheck(b.Width != 960 || b.Height != 640, "disabled restart restored old geometry")
				}
				if mode == "layout-save" || mode == "layout-disable" {
					w.SetBounds(mygo.Rectangle{X: 120, Y: 120, Width: 960, Height: 640})
				}
				if mode == "layout-disable" {
					nativeCheck(s.setEnabled(false) == nil, "disable failed")
					// Reopening an editor must not reuse captures made while disabled.
					s.states["editor"] = mygo.WindowState{Width: 1100, Height: 700}
					prepareWindowState("editor")
					e := mygo.NewWindow(mygo.WindowOptions{Title: "Editor", Width: 500, Height: 350, StateKey: "editor", Content: ui.View(func(*ui.Context) {})})
					nativeCheck(e.Bounds().Width != 1100, "disabled editor restored cached geometry")
					e.Destroy()
				}
				if mode == "layout-enable" {
					w.SetBounds(mygo.Rectangle{X: 140, Y: 130, Width: 1020, Height: 680})
					nativeCheck(s.setEnabled(true) == nil, "enable failed")
				}
			})
			time.Sleep(400 * time.Millisecond)
			mygo.App.Quit()
		}()
	})
	nativeCheck(mygo.App.Run() == nil, "native loop failed")
	if mode == "layout-disable" || mode == "layout-off" {
		data, err := os.ReadFile(s.path)
		nativeCheck(os.IsNotExist(err), fmt.Sprintf("disabled shutdown recreated geometry: %s %v", data, err))
	}
	fmt.Println("native geometry passed:", mode)
}
