package app

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func TestBackgroundNativeRendering(t *testing.T) {
	if os.Getenv("SSHYARD_BACKGROUND_NATIVE_TEST") != "1" {
		t.Skip("set SSHYARD_BACKGROUND_NATIVE_TEST=1 in a desktop session")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, exe, "-test.run=^$")
	cmd.Env = append(os.Environ(), "SSHYARD_BACKGROUND_NATIVE_HELPER=1", "SSHYARD_BACKGROUND_NATIVE_DIR="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err != nil || !bytes.Contains(out, []byte("native background passed")) {
		t.Fatalf("native background subprocess: %v\n%s", err, out)
	}
	t.Log(string(out))
}

func backgroundNativeCheck(ok bool, message string) {
	if !ok {
		panic(message)
	}
}

// All native calls and app state access run on the main thread, while the
// driver waits off-thread; this isolated helper never opens the OS keychain.
func runBackgroundNative(dir string) {
	mygo.App.SetName("SSHYard Background Rendering Test")
	mygo.App.SetPath(mygo.PathUserData, dir)
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 240, G: 30, B: 20, A: 255})
		}
	}
	backgroundNativeCheck(os.MkdirAll(filepath.Join(dir, "backgrounds"), 0700) == nil, "create isolated background directory")
	f, err := os.Create(filepath.Join(dir, "backgrounds", "background-native.png"))
	backgroundNativeCheck(err == nil, "create managed fixture")
	backgroundNativeCheck(png.Encode(f, img) == nil, "encode managed fixture")
	backgroundNativeCheck(f.Close() == nil, "close managed fixture")
	s := settings.Default()
	s.CheckUpdates, s.MCPEnabled, s.UIZoom = false, false, 100
	s.Theme = theme.Light
	s.Background = settings.DefaultBackground()
	s.Background.Image = "background-native.png"
	s.Background.ChromeOpacity, s.Background.ContentOpacity = 0, 0
	backgroundNativeCheck(settings.Save(filepath.Join(dir, "settings.json"), s) == nil, "save isolated settings")
	done := make(chan struct{})
	mygo.App.WhenReady(func() {
		a, err := New(Platform{DataDir: dir, HomeDir: dir, CacheDir: filepath.Join(dir, "cache"), SecretLockPath: filepath.Join(dir, "secrets.lock"), SecretScope: "background-native-test", Secrets: keychain.NewMemory(), Post: func(fn func()) { go mygo.RunOnMain(fn) }})
		backgroundNativeCheck(err == nil, fmt.Sprintf("create isolated app: %v", err))
		w := mygo.NewWindow(mygo.WindowOptions{Title: "SSHYard Native Background Test", Width: 1000, Height: 720, Content: ui.View(a.View)})
		mygo.App.OnQuit(a.Close)
		a.Start()
		a.withSecrets(nil)
		go func() { defer close(done); driveBackgroundNative(a, w); mygo.App.Quit() }()
	})
	backgroundNativeCheck(mygo.App.Run() == nil, "run native app event loop")
	<-done
	fmt.Println("native background passed")
}

func backgroundNativeWait(what string, check func() bool) {
	deadline := time.Now().Add(12 * time.Second)
	for {
		ok := false
		mygo.RunOnMain(func() { ok = check() })
		if ok {
			return
		}
		backgroundNativeCheck(time.Now().Before(deadline), "timed out: "+what)
		time.Sleep(30 * time.Millisecond)
	}
}

// The sequential stages intentionally stay together so every native capture
// has a clear settings transition and all fixture data stays in the child.
func driveBackgroundNative(a *App, w *mygo.Window) {
	backgroundNativeWait("saved image async decode and unlock", func() bool { return a.unlocked() && a.hasBackground() })
	var cn *conn
	mygo.RunOnMain(func() {
		id, err := a.OpenLocalTerminal()
		backgroundNativeCheck(err == nil, fmt.Sprintf("open native local terminal: %v", err))
		cn = a.conns[id]
	})
	backgroundNativeWait("local terminal text renderer", func() bool {
		return cn.tv != nil && cn.s.Status().State == session.Connected && a.paneBounds[cn.id].W > 100
	})
	mygo.RunOnMain(func() {
		cn.tv.target.Terminal().WriteString("\x1b[2J\x1b[HNative SSHYard background\r\n\x1b[32mConnected\x1b[0m  \x1b[41m ANSI \x1b[0m\r\n$ echo native\r\nnative\r\n")
		w.Invalidate()
	})
	clear := backgroundNativeCapture(w, "native-clear-light", true)
	mygo.RunOnMain(func() {
		st := a.Settings()
		st.Theme = theme.Dark
		st.Background.ChromeOpacity, st.Background.ContentOpacity = 70, 80
		backgroundNativeCheck(a.ApplySettings(st) == nil, "apply dark defaults")
		w.SetSize(1150, 780)
		w.Invalidate()
	})
	tinted := backgroundNativeCapture(w, "native-default-dark-resized", false)
	backgroundNativeCheck(clear.Bounds().Dx() != tinted.Bounds().Dx() || clear.Bounds().Dy() != tinted.Bounds().Dy(), "native resize did not change captured geometry")
	mygo.RunOnMain(func() {
		st := a.Settings()
		st.Background.ChromeOpacity, st.Background.ContentOpacity = 100, 100
		backgroundNativeCheck(a.ApplySettings(st) == nil, "apply opaque regions")
		w.Invalidate()
	})
	opaque := backgroundNativeCapture(w, "native-opaque-dark", false)
	var width int
	mygo.RunOnMain(func() { width, _ = w.ContentSize() })
	p := color.RGBAModel.Convert(opaque.At(40*opaque.Bounds().Dx()/width, 300*opaque.Bounds().Dx()/width)).(color.RGBA)
	tok := theme.DarkTokens()
	backgroundNativeCheck(p.R == tok.Sidebar.R && p.G == tok.Sidebar.G && p.B == tok.Sidebar.B, fmt.Sprintf("opaque chrome still exposes image: %v want %v", p, tok.Sidebar))
	var terminalBackground ui.Color
	mygo.RunOnMain(func() { terminalBackground = a.termPalette(tok).Background })
	p = color.RGBAModel.Convert(opaque.At(750*opaque.Bounds().Dx()/width, 550*opaque.Bounds().Dx()/width)).(color.RGBA)
	backgroundNativeCheck(p.R == terminalBackground.R && p.G == terminalBackground.G && p.B == terminalBackground.B, fmt.Sprintf("opaque terminal still exposes image: %v want %v", p, terminalBackground))
}

func backgroundNativeCapture(w *mygo.Window, name string, clear bool) image.Image {
	// Wait for native placement/resize and invalidated content to reach a frame.
	time.Sleep(350 * time.Millisecond)
	data, err := w.CapturePage()
	backgroundNativeCheck(err == nil && len(data) > 0, fmt.Sprintf("capture %s: %v", name, err))
	img, err := png.Decode(bytes.NewReader(data))
	backgroundNativeCheck(err == nil && img.Bounds().Dx() > 0 && img.Bounds().Dy() > 0, "decode native screenshot")
	var width int
	mygo.RunOnMain(func() { width, _ = w.ContentSize() })
	backgroundNativeCheck(width > 0, "native content has no width")
	if clear {
		scale := float32(img.Bounds().Dx()) / float32(width)
		p := color.RGBAModel.Convert(img.At(int(40*scale), int(300*scale))).(color.RGBA)
		backgroundNativeCheck(p.R > 220 && p.G < 45 && p.B < 40, fmt.Sprintf("native sidebar did not reveal managed image: %v", p))
		p = color.RGBAModel.Convert(img.At(int(750*scale), int(550*scale))).(color.RGBA)
		backgroundNativeCheck(p.R > 220 && p.G < 45 && p.B < 40, fmt.Sprintf("native terminal did not reveal managed image: %v", p))
	}
	if dir := os.Getenv("SSHYARD_BACKGROUND_SHOTS"); dir != "" {
		backgroundNativeCheck(os.MkdirAll(dir, 0700) == nil, "create native screenshots directory")
		backgroundNativeCheck(os.WriteFile(filepath.Join(dir, name+".png"), data, 0600) == nil, "save native screenshot")
	}
	return img
}
