package app

import (
	"bytes"
	"errors"
	"image"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
)

const backgroundBytes = 20 << 20
const backgroundPixels = 32_000_000

var (
	errBackgroundUnsupported = errors.New("unsupported background image")
	errBackgroundTooLarge    = errors.New("background image too large")
	errBackgroundStorage     = errors.New("background storage unavailable")
)

type backgroundState struct {
	preview   *settings.Background
	bmp       *ui.Bitmap
	busy      bool
	err       error
	requested string
	active    string
	seq       uint64
	// pending files belong to workers until their UI callback commits them.
	mu      sync.Mutex
	pending map[string]bool
	closed  bool
}

func (a *App) hasBackground() bool {
	return a.background.bmp != nil && a.background.active == a.settings.Background.Image && a.background.active != ""
}

func (a *App) backgroundDir() (string, error) {
	if a.p.DataDir == "" {
		return "", errBackgroundStorage
	}
	return filepath.Join(a.p.DataDir, "backgrounds"), nil
}

// ensureBackground schedules one decode for each saved image, including failures.
func (a *App) ensureBackground() {
	b, name := &a.background, a.settings.Background.Image
	if b.busy || b.requested == name {
		return
	}
	b.seq++
	seq := b.seq
	b.requested, b.active, b.bmp, b.err = name, "", nil, nil
	if name == "" {
		return
	}
	b.busy = true
	go func() {
		dir, err := a.backgroundDir()
		var bmp *ui.Bitmap
		if err == nil && !settings.BackgroundImageOK(name) {
			err = errBackgroundUnsupported
		}
		if err == nil {
			_, bmp, _, err = readBackground(filepath.Join(dir, name))
		}
		a.post(func() {
			if b.seq != seq || a.settings.Background.Image != name {
				if b.seq == seq {
					b.busy = false
				}
				return
			}
			if err != nil {
				lang := a.settings.Language
				if lang == "" {
					lang = i18n.Locale()
				}
				err = errors.New(locales.In(lang).BackgroundLoadFailed(backgroundError(locales.In(lang), err)))
				a.toastError(err.Error())
			}
			b.busy, b.err, b.bmp = false, err, bmp
			if err == nil {
				b.active = name
			}
		})
	}()
}

// readBackground checks encoded size, format and dimensions before allocating pixels.
func readBackground(path string) ([]byte, *ui.Bitmap, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, backgroundBytes+1))
	if err != nil {
		return nil, nil, "", err
	}
	if len(data) > backgroundBytes {
		return nil, nil, "", errBackgroundTooLarge
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, nil, "", err
	}
	if format != "png" && format != "jpeg" && format != "webp" {
		return nil, nil, "", errBackgroundUnsupported
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width) > backgroundPixels/int64(cfg.Height) {
		return nil, nil, "", errBackgroundTooLarge
	}
	bmp, err := ui.DecodeBitmap(data)
	return data, bmp, format, err
}

func backgroundError(m locales.Messages, err error) string {
	switch {
	case errors.Is(err, errBackgroundUnsupported):
		return m.BackgroundUnsupported()
	case errors.Is(err, errBackgroundTooLarge):
		return m.BackgroundTooLarge()
	case errors.Is(err, errBackgroundStorage):
		return m.BackgroundStorageUnavailable()
	default:
		return err.Error()
	}
}

func (a *App) pickBackground(m locales.Messages) {
	if a.background.busy || a.p.PickFile == nil {
		return
	}
	b := &a.background
	b.seq++
	seq := b.seq
	b.busy = true
	a.setBackgroundPreview(nil)
	go func() {
		path, err := a.p.PickFile(m.BackgroundChoose())
		var bmp *ui.Bitmap
		var managed string
		if err == nil && path != "" {
			managed, bmp, err = a.importBackground(path)
		}
		a.post(func() { a.backgroundImported(m, seq, managed, bmp, err) })
	}()
}

func (a *App) importBackground(path string) (string, *ui.Bitmap, error) {
	data, bmp, format, err := readBackground(path)
	if err != nil {
		return "", nil, err
	}
	dir, err := a.backgroundDir()
	if err != nil {
		return "", nil, err
	}
	b := &a.background
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return "", nil, errBackgroundStorage
	}
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return "", nil, err
	}
	f, err := os.CreateTemp(dir, "background-*."+format)
	if err != nil {
		return "", nil, err
	}
	name := f.Name()
	_, err = f.Write(data)
	err = errors.Join(err, f.Close())
	if err != nil {
		return "", nil, errors.Join(err, deleteBackground(name))
	}
	if b.pending == nil {
		b.pending = make(map[string]bool)
	}
	b.pending[name] = true
	return name, bmp, nil
}

func (a *App) backgroundImported(m locales.Messages, seq uint64, managed string, bmp *ui.Bitmap, err error) {
	b := &a.background
	b.mu.Lock()
	delete(b.pending, managed)
	b.mu.Unlock()
	if b.seq != seq {
		if cleanup := deleteBackground(managed); cleanup != nil {
			a.toastError(m.BackgroundImportFailed(cleanup.Error()))
		}
		return
	}
	b.busy, b.preview = false, nil
	if err == nil && managed == "" {
		return
	} // Native picker cancellation.
	old := a.settings.Background.Image
	if err == nil {
		s := a.settings
		s.Background.Image = filepath.Base(managed)
		err = a.ApplySettings(s)
	}
	if err != nil {
		err = errors.Join(err, deleteBackground(managed))
		b.err = errors.New(m.BackgroundImportFailed(backgroundError(m, err)))
		a.toastError(b.err.Error())
		return
	}
	b.requested, b.active, b.bmp, b.err = a.settings.Background.Image, a.settings.Background.Image, bmp, nil
	if old != "" && old != b.active {
		a.cleanupBackground(m, old)
	}
}

func (a *App) removeBackground(m locales.Messages) {
	a.setBackgroundPreview(nil)
	s := a.settings
	old := s.Background.Image
	s.Background.Image = ""
	if err := a.ApplySettings(s); err != nil {
		a.toastError(m.ErrGeneric(err.Error()))
		return
	}
	b := &a.background
	b.seq++
	b.requested, b.active, b.bmp, b.busy, b.err = "", "", nil, false, nil
	a.cleanupBackground(m, old)
}

func deleteBackground(path string) error {
	if path == "" {
		return nil
	}
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (a *App) cleanupBackground(m locales.Messages, name string) {
	if !settings.BackgroundImageOK(name) {
		return
	}
	dir, err := a.backgroundDir()
	if err == nil {
		err = deleteBackground(filepath.Join(dir, name))
	}
	if err != nil {
		a.background.err = errors.New(m.BackgroundImportFailed(err.Error()))
		a.toastError(a.background.err.Error())
	}
}

func (a *App) closeBackground() {
	b := &a.background
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	b.preview = nil
	for path := range b.pending {
		if err := deleteBackground(path); err != nil {
			log.Printf("sshyard: background cleanup failed: %v", err)
		}
		delete(b.pending, path)
	}
}
