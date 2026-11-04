// Package gist keeps backups in a secret GitHub gist, reached with a
// personal access token: each backup is a file of its own, named by
// backup.Name, in the one gist whose description is Description. A secret
// gist is unlisted, not private: anyone with its address can read the
// sealed files in it.
package gist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Licoy/sshyard/internal/backup"
)

// Description marks the gist SSHYard keeps its backups in.
const Description = "SSHYard backup"

// defaultAPI is GitHub's REST API.
const defaultAPI = "https://api.github.com"

// maxPages bounds the pages of the user's gists read to find the backup
// gist, of 100 gists each.
const maxPages = 10

// maxMessage bounds the error message kept from an answer, in runes.
const maxMessage = 200

var (
	// ErrToken reports an empty token, or one with spaces or line breaks.
	ErrToken = errors.New("gist: empty or malformed token")
	// ErrName reports a file name that is not a backup's.
	ErrName = errors.New("gist: not a backup file name")
	// ErrText reports data that is not UTF-8 text, which a gist cannot hold.
	ErrText = errors.New("gist: a gist holds text only")
)

// Gist is the backup gist of the user a token belongs to.
type Gist struct {
	Token string
	// ID is the gist's ID, "" until one is found or made.
	ID string
	// Bound is called, on the calling goroutine, with the ID once a gist
	// is found or made, so that it can be saved. It may be nil.
	Bound func(id string)
	// API is the REST API's root; "" is GitHub's.
	API string
	// HTTP is the client requests go through; nil is backup.Client(nil).
	HTTP *http.Client
}

var _ backup.Remote = (*Gist)(nil)

// file is a file of a gist as the API gives it. Content is only in a
// single gist's answer, and cut short when Truncated (over 1 MB).
type file struct {
	Size      int64  `json:"size"`
	Truncated bool   `json:"truncated"`
	RawURL    string `json:"raw_url"`
	Content   string `json:"content"`
}

// doc is a gist as the API gives it.
type doc struct {
	ID          string           `json:"id"`
	Description string           `json:"description"`
	UpdatedAt   time.Time        `json:"updated_at"`
	Files       map[string]*file `json:"files"`
}

func (g *Gist) api() string {
	if g.API == "" {
		return defaultAPI
	}
	return strings.TrimRight(g.API, "/")
}

func (g *Gist) client() *http.Client {
	if g.HTTP != nil {
		return g.HTTP
	}
	return backup.Client(nil)
}

func (g *Gist) tokenOK() bool {
	return g.Token != "" && !strings.ContainsAny(g.Token, " \t\r\n\x00")
}

// validID reports whether s can be a gist's ID: letters and digits only,
// so that it stays one segment of a path.
func validID(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// errID reports an ID that cannot name a gist.
var errID = fmt.Errorf("gist: bad ID: %w", backup.ErrNotFound)

// do sends one API request with the token, body as JSON when not nil, and
// decodes a success into out when not nil.
func (g *Gist) do(ctx context.Context, method, path string, body, out any) error {
	if !g.tokenOK() {
		return ErrToken
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, g.api()+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+g.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "SSHYard")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := backup.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return statusError(resp.StatusCode, b)
	}
	if err != nil {
		return err
	}
	if out != nil && json.Unmarshal(b, out) != nil {
		return errors.New("gist: unexpected answer from " + req.URL.Host)
	}
	return nil
}

// statusError is a failed answer, with the message GitHub gave.
func statusError(status int, body []byte) error {
	var e struct {
		Message string `json:"message"`
	}
	json.Unmarshal(body, &e)
	msg := strings.TrimSpace(e.Message)
	if r := []rune(msg); len(r) > maxMessage {
		msg = string(r[:maxMessage]) + "…"
	}
	return &backup.StatusError{Status: status, Message: msg}
}

func (g *Gist) bind(id string) {
	g.ID = id
	if g.Bound != nil {
		g.Bound(id)
	}
}

// ensure finds the backup gist when no ID is known; with none found, the
// ID stays "".
func (g *Gist) ensure(ctx context.Context) error {
	if g.ID != "" {
		if !validID(g.ID) {
			return errID
		}
		return nil
	}
	id, err := g.find(ctx)
	if err != nil || id == "" {
		return err
	}
	g.bind(id)
	return nil
}

// find is the ID of the user's backup gist: the most recently updated with
// Description, or failing that one holding a backup's file; "" for none.
func (g *Gist) find(ctx context.Context) (string, error) {
	var best, fallback *doc
	for page := 1; page <= maxPages; page++ {
		var docs []doc
		if err := g.do(ctx, http.MethodGet, "/gists?per_page=100&page="+strconv.Itoa(page), nil, &docs); err != nil {
			return "", err
		}
		for i := range docs {
			d := &docs[i]
			if !validID(d.ID) {
				continue
			}
			if d.Description == Description {
				if best == nil || d.UpdatedAt.After(best.UpdatedAt) {
					best = d
				}
				continue
			}
			for name := range d.Files {
				if _, _, ok := backup.Parse(name); ok && (fallback == nil || d.UpdatedAt.After(fallback.UpdatedAt)) {
					fallback = d
					break
				}
			}
		}
		if len(docs) < 100 {
			break
		}
	}
	switch {
	case best != nil:
		return best.ID, nil
	case fallback != nil:
		return fallback.ID, nil
	}
	return "", nil
}

// get reads the backup gist, its files with their contents.
func (g *Gist) get(ctx context.Context) (doc, error) {
	var d doc
	err := g.do(ctx, http.MethodGet, "/gists/"+g.ID, nil, &d)
	return d, err
}

// Check reaches the backup gist with the token, or, with none yet, the
// user's gists.
func (g *Gist) Check(ctx context.Context) error {
	if err := g.ensure(ctx); err != nil || g.ID == "" {
		// find read the user's gists, which is the token's check.
		return err
	}
	_, err := g.get(ctx)
	return err
}

// List is the backups in the gist, newest first; none without a gist.
func (g *Gist) List(ctx context.Context) ([]backup.Object, error) {
	if err := g.ensure(ctx); err != nil || g.ID == "" {
		return nil, err
	}
	d, err := g.get(ctx)
	if err != nil {
		return nil, err
	}
	var out []backup.Object
	for name, f := range d.Files {
		if f == nil {
			continue
		}
		if o, ok := backup.ObjectOf(name, f.Size); ok {
			out = append(out, o)
		}
	}
	backup.SortNewest(out)
	return out, nil
}

// Get reads one backup, from its raw address when the API cut it short.
func (g *Gist) Get(ctx context.Context, name string) ([]byte, error) {
	if _, _, ok := backup.Parse(name); !ok {
		return nil, backup.ErrNotFound
	}
	if err := g.ensure(ctx); err != nil {
		return nil, err
	}
	if g.ID == "" {
		return nil, backup.ErrNotFound
	}
	d, err := g.get(ctx)
	if err != nil {
		return nil, err
	}
	f := d.Files[name]
	if f == nil {
		return nil, backup.ErrNotFound
	}
	if !f.Truncated {
		if len(f.Content) > backup.MaxSize {
			return nil, backup.ErrTooLarge
		}
		return []byte(f.Content), nil
	}
	return g.raw(ctx, f.RawURL)
}

// raw reads a file's raw address: over https, or at the API's own scheme
// and host. The token goes only to the API's host.
func (g *Gist) raw(ctx context.Context, raw string) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, errors.New("gist: bad raw address")
	}
	api, err := url.Parse(g.api())
	if err != nil {
		return nil, err
	}
	same := u.Scheme == api.Scheme && strings.EqualFold(u.Host, api.Host)
	if u.Scheme != "https" && !same {
		return nil, errors.New("gist: refusing raw address on " + u.Scheme + "://" + u.Host)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "SSHYard")
	if same {
		if !g.tokenOK() {
			return nil, ErrToken
		}
		req.Header.Set("Authorization", "Bearer "+g.Token)
	}
	resp, err := g.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := backup.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, statusError(resp.StatusCode, b)
	}
	return b, err
}

// Put adds a backup to the gist, making the gist (secret) when there is
// none yet.
func (g *Gist) Put(ctx context.Context, name string, data []byte) error {
	if _, _, ok := backup.Parse(name); !ok {
		return ErrName
	}
	if !utf8.Valid(data) {
		return ErrText
	}
	if !g.tokenOK() {
		return ErrToken
	}
	if err := g.ensure(ctx); err != nil {
		return err
	}
	files := map[string]any{name: map[string]string{"content": string(data)}}
	if g.ID != "" {
		return g.do(ctx, http.MethodPatch, "/gists/"+g.ID, map[string]any{"files": files}, nil)
	}
	var d doc
	if err := g.do(ctx, http.MethodPost, "/gists", map[string]any{"description": Description, "public": false, "files": files}, &d); err != nil {
		return err
	}
	if !validID(d.ID) {
		return errors.New("gist: unexpected answer making the gist")
	}
	g.bind(d.ID)
	return nil
}

// Delete removes a backup from the gist. Its earlier revisions keep it.
func (g *Gist) Delete(ctx context.Context, name string) error {
	if _, _, ok := backup.Parse(name); !ok {
		return ErrName
	}
	if err := g.ensure(ctx); err != nil {
		return err
	}
	if g.ID == "" {
		return backup.ErrNotFound
	}
	return g.do(ctx, http.MethodPatch, "/gists/"+g.ID, map[string]any{"files": map[string]any{name: nil}}, nil)
}
