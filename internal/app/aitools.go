package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/Licoy/sshyard/internal/ai/remote"
	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/sftpsvc"
)

// hostTools runs one pane's tools on the host's pooled SSH connection.
// snap runs on the UI thread before the turn goroutine starts. That
// goroutine is the only one that dials, runs commands, and closes.
type hostTools struct {
	a         *App
	c         *conn
	host      assets.Host
	run       *remote.Runner
	files     *remote.Files
	sftp      *sftpsvc.Client
	release   func()
	vers      map[string]sftpsvc.Version
	wantBegin bool
	counted   bool
	nbytes    int64
}

func newHostTools(a *App, c *conn) *hostTools {
	return &hostTools{a: a, c: c, host: c.host, vers: map[string]sftpsvc.Version{}}
}

func (h *hostTools) snap() { h.host = h.c.host }

// noteTurn starts the turn clock. It dials only when a connection is
// already up, so a chat that never calls a remote tool does not connect.
func (h *hostTools) noteTurn(ctx context.Context) {
	h.wantBegin = true
	if h.a.pool.Shared(h.a.poolKey(h.host)) > 0 {
		_ = h.connect(ctx)
	}
}

func (h *hostTools) end() {
	if h.files != nil {
		h.files.Close()
		h.files = nil
	}
	if h.sftp != nil {
		h.sftp.Close()
		h.sftp = nil
	}
	if h.release != nil {
		h.release()
		h.release = nil
	}
	h.run = nil
	h.vers = map[string]sftpsvc.Version{}
	h.wantBegin = false
}

func (h *hostTools) connect(ctx context.Context) error {
	if h.run != nil {
		if h.wantBegin {
			h.run.BeginTurn()
			h.wantBegin = false
		}
		return nil
	}
	cfg := h.a.hostConfig(h.host, h.c, func(context.Context) (session.Credentials, error) {
		return h.a.credentials(h.c, h.host)
	})
	client, release, err := h.a.pool.Acquire(ctx, h.a.poolKey(h.host), cfg)
	if err != nil {
		return err
	}
	files, err := remote.OpenFiles(client)
	if err != nil {
		release()
		return err
	}
	sftp, err := sftpsvc.New(client)
	if err != nil {
		files.Close()
		release()
		return err
	}
	h.run = remote.New(client, remote.Options{})
	h.files = files
	h.sftp = sftp
	h.release = release
	if h.wantBegin {
		h.run.BeginTurn()
		h.wantBegin = false
	}
	return nil
}

// OutputBytes is the last command's stdout plus stderr, as counted by
// CountingWriter. A negative value means the last call was not a command.
func (h *hostTools) OutputBytes() int64 {
	if !h.counted {
		return -1
	}
	return h.nbytes
}

func (h *hostTools) ReadFile(ctx context.Context, p string) (string, bool, error) {
	if err := h.connect(ctx); err != nil {
		if ctx.Err() != nil {
			return "", false, ctx.Err()
		}
		return "", false, err
	}
	b, ver, err := h.files.Read(p)
	if errors.Is(err, fs.ErrNotExist) {
		delete(h.vers, p)
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	h.vers[p] = ver
	return string(b), true, nil
}

type toolArgs struct {
	Command string `json:"command"`
	Dir     string `json:"dir"`
	Path    string `json:"path"`
	Content string `json:"content"`
	Query   string `json:"query"`
	Lines   int    `json:"lines"`
}

func (h *hostTools) Call(ctx context.Context, name string, raw json.RawMessage) (string, int, error) {
	h.counted = false
	var args toolArgs
	if len(bytes.TrimSpace(raw)) > 0 {
		_ = json.Unmarshal(raw, &args)
	}
	switch name {
	case "host_info":
		return fmt.Sprintf("name: %s\naddress: %s\nuser: %s", h.host.Label(), h.host.Addr(), h.host.User), 0, nil
	case "pane_output":
		return trimLines(h.a.aiOutput(h.c), args.Lines), 0, nil
	case "list_dir", "read_file", "search_files", "write_file", "run_command":
		if err := h.connect(ctx); err != nil {
			return toolErr(ctx, err)
		}
	default:
		return "unknown tool " + name, 1, nil
	}
	switch name {
	case "list_dir":
		return h.list(args.Path)
	case "read_file":
		text, ok, err := h.ReadFile(ctx, args.Path)
		if err != nil {
			return toolErr(ctx, err)
		}
		if !ok {
			return "no such file", 1, nil
		}
		return text, 0, nil
	case "search_files":
		return h.search(args.Path, args.Query)
	case "write_file":
		return h.write(args.Path, args.Content)
	default:
		return h.command(ctx, args.Command, args.Dir)
	}
}

func toolErr(ctx context.Context, err error) (string, int, error) {
	if ctx.Err() != nil {
		return "", -1, ctx.Err()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "", -1, err
	}
	return err.Error(), 1, nil
}

func trimLines(text string, n int) string {
	if n <= 0 {
		return text
	}
	lines := strings.Split(text, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func (h *hostTools) list(dir string) (string, int, error) {
	es, err := h.sftp.List(dir)
	if err != nil {
		return err.Error(), 1, nil
	}
	var b strings.Builder
	for _, e := range es {
		b.WriteString(e.Name)
		if e.IsDir {
			b.WriteByte('/')
		}
		b.WriteByte('\n')
	}
	if b.Len() == 0 {
		return "(empty)", 0, nil
	}
	return b.String(), 0, nil
}

// shortcut: one directory, not a tree, until a caller needs a walk.
func (h *hostTools) search(dir, query string) (string, int, error) {
	if query == "" {
		return "empty query", 1, nil
	}
	es, err := h.sftp.List(dir)
	if err != nil {
		return err.Error(), 1, nil
	}
	var b strings.Builder
	n, total := 0, 0
	const maxFiles, maxBytes = 20, 256 << 10
	for _, e := range es {
		if e.IsDir || n >= maxFiles || total >= maxBytes {
			continue
		}
		data, _, err := h.files.Read(path.Join(dir, e.Name))
		if err != nil {
			continue
		}
		total += len(data)
		if !bytes.Contains(data, []byte(query)) {
			continue
		}
		b.WriteString(path.Join(dir, e.Name))
		b.WriteByte('\n')
		n++
	}
	if b.Len() == 0 {
		return "no matches", 0, nil
	}
	return b.String(), 0, nil
}

func (h *hostTools) write(p, content string) (string, int, error) {
	was := h.vers[p]
	if _, err := h.files.Write(p, []byte(content), was); err != nil {
		if errors.Is(err, sftpsvc.ErrChanged) {
			return "the file changed on the server; nothing was written", 1, nil
		}
		return err.Error(), 1, nil
	}
	delete(h.vers, p)
	return "wrote " + p, 0, nil
}

func (h *hostTools) command(ctx context.Context, cmd, dir string) (string, int, error) {
	var outB, errB bytes.Buffer
	cwOut := &remote.CountingWriter{W: &outB}
	cwErr := &remote.CountingWriter{W: &errB}
	code, err := h.run.Run(ctx, cmd, dir, cwOut, cwErr)
	h.nbytes = cwOut.Bytes() + cwErr.Bytes()
	h.counted = true
	text := joinOut(outB.String(), errB.String())
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return text, -1, err
		}
		text = joinOut(text, err.Error())
		if code == 0 {
			code = 1
		}
		return text, code, nil
	}
	return text, code, nil
}

func joinOut(a, b string) string {
	if b == "" {
		return a
	}
	if a == "" {
		return b
	}
	if strings.HasSuffix(a, "\n") {
		return a + b
	}
	return a + "\n" + b
}
