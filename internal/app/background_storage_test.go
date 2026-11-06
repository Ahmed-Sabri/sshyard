package app

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

func backgroundFixture(t *testing.T, format string) string {
	t.Helper()
	var out bytes.Buffer
	img := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	var err error
	switch format {
	case "png":
		err = png.Encode(&out, img)
	case "jpeg":
		err = jpeg.Encode(&out, img, nil)
	case "gif":
		err = gif.Encode(&out, img, nil)
	case "webp":
		// Same 1x1 fixture used by MyGo ui.TestDecodeBitmapOfWebPAndBMP
		var data []byte
		data, err = base64.StdEncoding.DecodeString("UklGRiQAAABXRUJQVlA4IBgAAAAwAQCdASoBAAEAAwA0JaQAA3AA/vuUAAA=")
		if err == nil {
			_, err = out.Write(data)
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "original."+format)
	if err := os.WriteFile(path, out.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBackgroundImageLimits(t *testing.T) {
	for _, format := range []string{"png", "jpeg", "webp"} {
		data, bmp, got, err := readBackground(backgroundFixture(t, format))
		if err != nil || len(data) == 0 || bmp == nil || got != format {
			t.Fatalf("%s: %s %v", format, got, err)
		}
	}
	if _, _, _, err := readBackground(backgroundFixture(t, "gif")); !errors.Is(err, errBackgroundUnsupported) {
		t.Fatalf("GIF: %v", err)
	}
	corrupt := backgroundFixture(t, "png")
	if err := os.WriteFile(corrupt, []byte("\x89PNG\r\n\x1a\ntruncated"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, bmp, _, err := readBackground(corrupt); err == nil || bmp != nil {
		t.Fatal("corrupt image accepted")
	}
	path := backgroundFixture(t, "png")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	binary.BigEndian.PutUint32(data[16:20], backgroundPixels)
	binary.BigEndian.PutUint32(data[20:24], 2)
	binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := readBackground(path); !errors.Is(err, errBackgroundTooLarge) {
		t.Fatalf("pixels: %v", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	err = f.Truncate(backgroundBytes + 1)
	if closeErr := f.Close(); err != nil || closeErr != nil {
		t.Fatalf("truncate: %v %v", err, closeErr)
	}
	if _, _, _, err := readBackground(path); !errors.Is(err, errBackgroundTooLarge) {
		t.Fatalf("bytes: %v", err)
	}
}

func TestBackgroundManagedPersistence(t *testing.T) {
	a, tt := newTestApp(t)
	m := locales.In("en")
	source := backgroundFixture(t, "png")
	managed, bmp, err := a.importBackground(source)
	if err != nil {
		t.Fatal(err)
	}
	a.backgroundImported(m, a.background.seq, managed, bmp, nil)
	if !a.hasBackground() {
		t.Fatalf("not active: %v", a.background.err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	saved, err := settings.Load(filepath.Join(a.p.DataDir, "settings.json"))
	if err != nil || saved.Background.Image != filepath.Base(managed) {
		t.Fatalf("saved: %+v %v", saved.Background, err)
	}
	// A fresh cache loads the managed file after the source has moved away.
	a.background.bmp, a.background.active, a.background.requested = nil, "", ""
	a.ensureBackground()
	waitFor(t, tt, "background cached", func() bool { return !a.background.busy })
	if !a.hasBackground() {
		t.Fatalf("reload: %v", a.background.err)
	}
	cached := a.background.bmp
	if err := os.Remove(managed); err != nil {
		t.Fatal(err)
	}
	a.ensureBackground()
	if a.background.bmp != cached || a.background.busy {
		t.Fatal("cached image decoded again")
	}
	// A missing file reports once and ordinary frames never retry it.
	a.background.bmp, a.background.active, a.background.requested = nil, "", ""
	a.ensureBackground()
	waitFor(t, tt, "background load failure", func() bool { return !a.background.busy })
	if a.background.err == nil || a.hasBackground() {
		t.Fatal("missing file did not fail visibly")
	}
	seq := a.background.seq
	a.ensureBackground()
	if a.background.seq != seq || a.background.busy {
		t.Fatal("failed file retried on a frame")
	}
}

func TestBackgroundImportPreservesOnFailure(t *testing.T) {
	a, _ := newTestApp(t)
	m := locales.In("en")
	managed, bmp, err := a.importBackground(backgroundFixture(t, "png"))
	if err != nil {
		t.Fatal(err)
	}
	a.backgroundImported(m, a.background.seq, managed, bmp, nil)
	old := a.settings.Background
	oldBMP := a.background.bmp
	a.backgroundImported(m, a.background.seq, "", nil, nil)
	if a.settings.Background != old || a.background.bmp != oldBMP {
		t.Fatal("cancel changed background")
	}
	_, _, invalid := a.importBackground(backgroundFixture(t, "gif"))
	a.backgroundImported(m, a.background.seq, "", nil, invalid)
	if a.settings.Background != old || a.background.bmp != oldBMP || a.background.err == nil {
		t.Fatal("invalid import lost background or error")
	}
	other, otherBMP, err := a.importBackground(backgroundFixture(t, "jpeg"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(a.p.DataDir, "settings.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	a.backgroundImported(m, a.background.seq, other, otherBMP, nil)
	if a.settings.Background != old || a.background.bmp != oldBMP || a.background.err == nil {
		t.Fatal("save failure lost background")
	}
	if _, err := os.Stat(other); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed import copy remains: %v", err)
	}
	if _, err := os.Stat(managed); err != nil {
		t.Fatalf("old managed copy lost: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	a.removeBackground(m)
	if a.hasBackground() || a.settings.Background.Image != "" {
		t.Fatal("remove retained image")
	}
	if _, err := os.Stat(managed); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed copy remains: %v", err)
	}
}

func TestBackgroundStaleAndShutdownCleanup(t *testing.T) {
	a, _ := newTestApp(t)
	m := locales.In("en")
	managed, bmp, err := a.importBackground(backgroundFixture(t, "png"))
	if err != nil {
		t.Fatal(err)
	}
	a.background.seq++
	a.backgroundImported(m, a.background.seq-1, managed, bmp, nil)
	if _, err := os.Stat(managed); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale copy remains: %v", err)
	}
	managed, _, err = a.importBackground(backgroundFixture(t, "png"))
	if err != nil {
		t.Fatal(err)
	}
	a.closeBackground()
	if _, err := os.Stat(managed); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pending shutdown copy remains: %v", err)
	}
	if _, _, err := a.importBackground(backgroundFixture(t, "png")); !errors.Is(err, errBackgroundStorage) {
		t.Fatalf("import after close: %v", err)
	}
}

func TestBackgroundBackupDeviceLocal(t *testing.T) {
	a, _ := newTestApp(t)
	local := settings.Background{Image: "background-local.png", Fit: "contain", ChromeOpacity: 0, ContentOpacity: 100}
	a.settings.Background = local
	p, err := a.collectBackup(false)
	if err != nil {
		t.Fatal(err)
	}
	if p.Settings.Background.Image != "" {
		t.Fatal("backup leaked device background reference")
	}
	// Packing also strips local paths from explicitly constructed payloads.
	p.Settings.Background = local
	plain, err := a.packBackup(p)
	if err != nil {
		t.Fatal(err)
	}
	got, err := unpackBackup(plain)
	if err != nil || got.Settings.Background.Image != "" {
		t.Fatalf("packed background: %+v %v", got.Settings.Background, err)
	}
	incoming := settings.Default()
	incoming.Background.Image = "background-other.png"
	if merged := mergeSettings(a.settings, incoming); merged.Background != local {
		t.Fatalf("restore changed local background: %+v", merged.Background)
	}
}

func TestBackgroundLoadFailureToastOnce(t *testing.T) {
	a, tt := newTestApp(t)
	a.settings.Background.Image = "background-missing.png"
	a.ensureBackground()
	waitFor(t, tt, "background load error toast", func() bool {
		return !a.background.busy && a.background.err != nil && tt.HasText(a.background.err.Error())
	})
	seq := a.background.seq
	for range 3 {
		a.ensureBackground()
		a.drain()
		if len(a.toasts) != 0 {
			t.Fatal("load failure queued another toast on an ordinary frame")
		}
		tt.Frame()
	}
	if a.background.seq != seq {
		t.Fatal("failed background retried")
	}
}

func TestBackgroundPickerCancelKeepsLoadError(t *testing.T) {
	a, tt := newTestApp(t)
	a.settings.Background.Image = "background-missing.png"
	a.ensureBackground()
	waitFor(t, tt, "background load failure", func() bool { return !a.background.busy && a.background.err != nil })
	old, oldErr, oldBMP := a.settings.Background, a.background.err, a.background.bmp
	a.p.PickFile = func(string) (string, error) { return "", nil }
	a.pickBackground(locales.In("en"))
	if a.background.err != oldErr {
		t.Fatal("opening picker cleared the previous load error")
	}
	waitFor(t, tt, "background picker cancel", func() bool { return !a.background.busy })
	if a.settings.Background != old || a.background.err != oldErr || a.background.bmp != oldBMP {
		t.Fatal("cancel changed the failed background state")
	}
}

func TestBackgroundApplySettingsInvalidatesWindows(t *testing.T) {
	a, _ := newTestApp(t)
	var svc ui.Services
	idle := ui.NewTester(func(c *ui.Context) { svc = c.Services() }, 100, 100)
	idle.Frame()
	a.mu.Lock()
	if a.winSvc == nil {
		a.winSvc = make(map[int]ui.Services)
	}
	a.winSvc[-9001] = svc
	a.mu.Unlock()
	t.Cleanup(func() { a.mu.Lock(); delete(a.winSvc, -9001); a.mu.Unlock() })
	for _, field := range []string{"fit", "chrome", "content", "image"} {
		s := a.settings
		switch field {
		case "fit":
			s.Background.Fit = "contain"
		case "chrome":
			s.Background.ChromeOpacity = 12
		case "content":
			s.Background.ContentOpacity = 23
		case "image":
			s.Background.Image = "background-saved.png"
		}
		draft := a.settings.Background
		a.background.preview = &draft
		idle.Frame()
		if err := a.ApplySettings(s); err != nil {
			t.Fatal(err)
		}
		if a.background.preview != nil || !idle.FramePending() {
			t.Fatalf("saved %s did not clear preview and invalidate idle window", field)
		}
	}
	idle.Frame()
	if err := a.ApplySettings(a.settings); err != nil {
		t.Fatal(err)
	}
	if idle.FramePending() {
		t.Fatal("unchanged background triggered redraw")
	}
}
