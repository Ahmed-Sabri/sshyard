package app

import (
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"
)

// testWindows are the windows an App opened, for tests: each shows its
// view in a Tester of its own.
type testWindows struct {
	mu   sync.Mutex
	wins []*testWindow
}

type testWindow struct {
	spec WindowSpec

	mu      sync.Mutex
	title   string
	edited  bool
	closed  bool
	focused int
}

func (w *testWindow) Focus() { w.mu.Lock(); w.focused++; w.mu.Unlock() }

// Close closes the window without asking, as the app does.
func (w *testWindow) Close() {
	w.mu.Lock()
	was := w.closed
	w.closed = true
	w.mu.Unlock()
	if !was && w.spec.Closed != nil {
		w.spec.Closed()
	}
}

func (w *testWindow) SetTitle(s string) { w.mu.Lock(); w.title = s; w.mu.Unlock() }
func (w *testWindow) SetEdited(b bool)  { w.mu.Lock(); w.edited = b; w.mu.Unlock() }

// userClose closes the window as its close button does: Closing may keep
// it open.
func (w *testWindow) userClose() bool {
	if w.spec.Closing != nil && !w.spec.Closing() {
		return false
	}
	w.Close()
	return true
}

func (w *testWindow) state() (title string, edited, closed bool, focused int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.title, w.edited, w.closed, w.focused
}

// fakeWindows makes the windows a opens test ones.
func fakeWindows(a *App) *testWindows {
	ws := &testWindows{}
	a.p.OpenWindow = func(s WindowSpec) Window {
		w := &testWindow{spec: s, title: s.Title}
		ws.mu.Lock()
		ws.wins = append(ws.wins, w)
		ws.mu.Unlock()
		return w
	}
	return ws
}

func (ws *testWindows) list() []*testWindow {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	return append([]*testWindow(nil), ws.wins...)
}

// tester shows the window's view.
func (w *testWindow) tester() *ui.Tester {
	tt := ui.NewTester(w.spec.View, 900, 640)
	tt.SetLocale("en")
	tt.Frame()
	return tt
}

// editorConn is the conn of the editor open, if one is.
func editorConn(a *App) *conn {
	for _, c := range a.conns {
		if c.ed != nil {
			return c
		}
	}
	return nil
}

// openedWindow waits for the app to open its n-th window and has the
// editor's conn and a tester showing it.
func openedWindow(t *testing.T, a *App, tt *ui.Tester, ws *testWindows, n int) (*testWindow, *ui.Tester, *conn) {
	t.Helper()
	waitFor(t, tt, "editor window", func() bool { return len(ws.list()) >= n })
	w := ws.list()[n-1]
	var ec *conn
	waitFor(t, tt, "the window known", func() bool {
		for _, c := range a.conns {
			if c.ed != nil && c.ed.win == Window(w) {
				ec = c
				return true
			}
		}
		return false
	})
	return w, w.tester(), ec
}
