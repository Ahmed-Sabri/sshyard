package app

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Licoy/sshyard/internal/ai/agent"
	"github.com/Licoy/sshyard/internal/ai/llm"
	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// The files a question can carry: images and PDFs go to the model as they
// are, text files go into the question.
const (
	maxAttachments  = 10
	maxAttachFile   = 20 << 20
	maxAttachText   = 512 << 10
	maxAttachTotal  = agent.MaxPartBytes
	attachThumbSize = 56
)

// aiAttach is a file attached to the question being written.
type aiAttach struct {
	name string
	mime string // of an image or PDF; "" for text
	data []byte
	bmp  *ui.Bitmap // an image's thumbnail, nil when it does not decode
}

func (f *aiAttach) text() bool  { return f.mime == "" }
func (f *aiAttach) image() bool { return strings.HasPrefix(f.mime, "image/") }

func (f *aiAttach) icon() *icons.Icon {
	switch {
	case f.image():
		return icons.Image
	case f.mime == "application/pdf":
		return icons.FilePDF
	case codeExt[strings.ToLower(filepath.Ext(f.name))]:
		return icons.FileCode
	}
	return icons.FileText
}

// codeExt are the extensions of source files, which show a code icon.
var codeExt = map[string]bool{
	".go": true, ".py": true, ".js": true, ".ts": true, ".tsx": true, ".jsx": true, ".rs": true, ".c": true,
	".h": true, ".cc": true, ".cpp": true, ".java": true, ".kt": true, ".swift": true, ".rb": true, ".php": true,
	".sh": true, ".bash": true, ".zsh": true, ".ps1": true, ".sql": true, ".json": true, ".yaml": true, ".yml": true,
	".toml": true, ".xml": true, ".html": true, ".css": true, ".lua": true, ".pl": true, ".conf": true, ".ini": true,
}

// attachMIME are the images and documents the models read.
var attachMIME = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true, "application/pdf": true,
}

// errAttach is why a file cannot be attached, as the toast says it.
type errAttach struct {
	msg func(m locales.Messages) string
}

func (e *errAttach) Error() string { return e.msg(locales.In("en")) }

// readAttach reads the file at path as an attachment, by what it holds:
// an image or PDF, or text, which is UTF-8 without NUL bytes. Anything
// else is refused.
func readAttach(path string) (*aiAttach, error) {
	name := filepath.Base(path)
	f, err := os.Open(path)
	if err != nil {
		return nil, &errAttach{func(m locales.Messages) string { return m.AiAttachReadFailed(name) }}
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		return nil, &errAttach{func(m locales.Messages) string { return m.AiAttachUnsupported(name) }}
	}
	data, err := io.ReadAll(io.LimitReader(f, maxAttachFile+1))
	if err != nil {
		return nil, &errAttach{func(m locales.Messages) string { return m.AiAttachReadFailed(name) }}
	}
	if len(data) > maxAttachFile {
		return nil, &errAttach{func(m locales.Messages) string { return m.AiAttachTooLarge(name, humanSize(maxAttachFile)) }}
	}
	return attachData(name, data)
}

func attachData(name string, data []byte) (*aiAttach, error) {
	mime, _, _ := strings.Cut(http.DetectContentType(data), ";")
	switch {
	case attachMIME[mime]:
		at := &aiAttach{name: name, mime: mime, data: data}
		if at.image() {
			at.bmp, _ = decodePreview(data)
		}
		return at, nil
	case len(data) > 0 && utf8.Valid(data) && !bytes.ContainsRune(data, 0):
		if len(data) > maxAttachText {
			return nil, &errAttach{func(m locales.Messages) string { return m.AiAttachTooLarge(name, humanSize(maxAttachText)) }}
		}
		return &aiAttach{name: name, data: data}, nil
	}
	return nil, &errAttach{func(m locales.Messages) string { return m.AiAttachUnsupported(name) }}
}

// addAttach attaches f to the pane's question, unless the question has
// as many files as it takes, or they would weigh too much, or the pane's
// agent cannot read it. It says why in a toast.
func (a *App) addAttach(cn *conn, f *aiAttach) {
	var why func(m locales.Messages) string
	total := len(f.data)
	for _, g := range cn.ai.attach {
		total += len(g.data)
	}
	switch {
	case cn.ai.agent == "acp" && !f.text():
		why = func(m locales.Messages) string { return m.AiAttachTextOnly(f.name) }
	case len(cn.ai.attach) >= maxAttachments:
		why = func(m locales.Messages) string { return m.AiAttachTooMany(strconv.Itoa(maxAttachments)) }
	case total > maxAttachTotal:
		why = func(m locales.Messages) string { return m.AiAttachTotal(humanSize(maxAttachTotal)) }
	}
	if why != nil {
		a.toast(func(m locales.Messages) ui.Toast { return ui.Toast{Title: why(m), Type: "error"} })
		return
	}
	cn.ai.attach = append(cn.ai.attach, f)
}

// attachPaths reads the files at paths, off the UI thread, and attaches
// them to the pane's question.
func (a *App) attachPaths(cn *conn, paths []string) {
	id := cn.id
	go func() {
		for _, p := range paths {
			f, err := readAttach(p)
			a.post(func() {
				cn := a.conns[id]
				if cn == nil || cn.ai == nil {
					return
				}
				var ea *errAttach
				if errors.As(err, &ea) {
					a.toast(func(m locales.Messages) ui.Toast { return ui.Toast{Title: ea.msg(m), Type: "error"} })
					return
				}
				a.addAttach(cn, f)
			})
		}
	}()
}

// pickAttach asks for files through the system's open dialog.
func (a *App) pickAttach(cn *conn, m locales.Messages) {
	if a.p.PickFiles == nil {
		return
	}
	id, title := cn.id, m.AiAttach()
	go func() {
		paths, err := a.p.PickFiles(title)
		if err != nil || len(paths) == 0 {
			return
		}
		a.post(func() {
			if cn := a.conns[id]; cn != nil && cn.ai != nil {
				a.attachPaths(cn, paths)
			}
		})
	}()
}

// attachTurn is what the question sends: text with the text files in it,
// as data from outside, and the images and PDFs as parts.
func attachTurn(text string, files []*aiAttach) (string, []llm.Part) {
	var parts []llm.Part
	for _, f := range files {
		if f.text() {
			if text != "" {
				text += "\n\n"
			}
			text += agent.WrapUntrusted("file "+f.name, string(f.data))
			continue
		}
		parts = append(parts, llm.Part{Name: f.name, MIME: f.mime, Data: f.data})
	}
	return text, parts
}

// attachStrip shows the files attached to a question: an image as its
// thumbnail, anything else as its type's icon and its name. With remove
// set, each has a button that takes it off.
func (a *App) attachStrip(c *ui.Context, tok theme.Tokens, m locales.Messages, files []*aiAttach, remove func(i int)) {
	ui.Row(c.Key("attach")).FillWidth().Wrap().Gap(theme.Space(1)).Children(func() {
		for i, f := range files {
			tile := ui.Row(c.Key("at:"+strconv.Itoa(i))).Height(attachThumbSize).Gap(theme.Space(1)).AlignItems(ui.Center).
				Radius(theme.RadiusControl).Background(tok.Inset).Border(1, tok.Line).Clip().Label(f.name)
			if f.bmp != nil {
				tile.Width(attachThumbSize)
			} else {
				tile.MaxWidth(200).Padding(0, theme.Space(1), 0, theme.Space(2))
			}
			tile.Children(func() {
				if f.bmp != nil {
					ui.Image(c, f.bmp).Size(attachThumbSize, attachThumbSize).Fit(ui.Cover).Label(f.name)
				} else {
					ui.Icon(c, f.icon().SVG).Size(18, 18).Shrink(0).TextColor(tok.TextMuted)
					ui.Column(c).Shrink(1).MinWidth(0).Children(func() {
						ui.Text(c, f.name).FontSize(theme.Footnote).SingleLine().Shrink(1)
						ui.Text(c, humanSize(int64(len(f.data)))).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine()
					})
				}
				if remove != nil {
					i := i
					if (IconButton{Icon: icons.Close, Label: m.AiRemoveAttachment(f.name), Size: Small}).Clicked(c, "rm") {
						remove(i)
					}
				}
			})
		}
	})
}
