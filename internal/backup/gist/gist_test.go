package gist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/backup"
)

const token = "ghp_secretTOKEN123"

// fakeGist is a gist the fake API keeps.
type fakeGist struct {
	desc    string
	updated time.Time
	files   map[string]string
}

// fakeGitHub is GitHub's gists API, in memory, for one token.
type fakeGitHub struct {
	t     *testing.T
	srv   *httptest.Server
	mu    sync.Mutex
	gists map[string]*fakeGist
	order []string // IDs, as the list gives them
	next  int
	// truncate cuts contents longer than this in a gist's answer (0: never)
	truncate int
	// rawBase is where raw addresses point; "" is the server itself
	rawBase string
	// requests are "METHOD path", rawAuth the Authorization of raw reads
	requests []string
	rawAuth  []string
	// nulls are the files a PATCH set to null
	nulls []string
}

func newFake(t *testing.T) *fakeGitHub {
	f := &fakeGitHub{t: t, gists: map[string]*fakeGist{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGitHub) add(desc string, updated time.Time, files map[string]string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	id := fmt.Sprintf("%032x", f.next)
	f.gists[id] = &fakeGist{desc: desc, updated: updated, files: files}
	f.order = append(f.order, id)
	return id
}

func (f *fakeGitHub) client(id string) *Gist {
	return &Gist{Token: token, ID: id, API: f.srv.URL}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (f *fakeGitHub) fileJSON(id, name, content string, withContent bool) map[string]any {
	out := map[string]any{"filename": name, "size": len(content), "raw_url": f.rawURL(id, name)}
	if withContent {
		if f.truncate > 0 && len(content) > f.truncate {
			out["content"], out["truncated"] = content[:f.truncate], true
		} else {
			out["content"], out["truncated"] = content, false
		}
	}
	return out
}

func (f *fakeGitHub) rawURL(id, name string) string {
	base := f.rawBase
	if base == "" {
		base = f.srv.URL
	}
	return base + "/raw/" + id + "/" + name
}

func (f *fakeGitHub) gistJSON(id string, withContent bool) map[string]any {
	g := f.gists[id]
	files := map[string]any{}
	for name, content := range g.files {
		files[name] = f.fileJSON(id, name, content, withContent)
	}
	return map[string]any{"id": id, "description": g.desc, "public": false, "updated_at": g.updated.Format(time.RFC3339), "files": files}
}

func (f *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	if rest, ok := strings.CutPrefix(r.URL.Path, "/raw/"); ok {
		f.rawAuth = append(f.rawAuth, r.Header.Get("Authorization"))
		id, name, _ := strings.Cut(rest, "/")
		g := f.gists[id]
		if g == nil || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		content, ok := g.files[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(content))
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+token {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "Bad credentials"})
		return
	}
	for h, want := range map[string]string{"Accept": "application/vnd.github+json", "X-GitHub-Api-Version": "2022-11-28", "User-Agent": "SSHYard"} {
		if got := r.Header.Get(h); got != want {
			f.t.Errorf("%s %s: %s = %q, want %q", r.Method, r.URL.Path, h, got, want)
		}
	}
	switch {
	case r.URL.Path == "/gists" && r.Method == http.MethodGet:
		per, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if per <= 0 {
			per = 30
		}
		if page <= 0 {
			page = 1
		}
		list := []any{}
		for i := (page - 1) * per; i < page*per && i < len(f.order); i++ {
			list = append(list, f.gistJSON(f.order[i], false))
		}
		writeJSON(w, http.StatusOK, list)
	case r.URL.Path == "/gists" && r.Method == http.MethodPost:
		var body struct {
			Description string `json:"description"`
			Public      *bool  `json:"public"`
			Files       map[string]struct {
				Content string `json:"content"`
			} `json:"files"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Public == nil || *body.Public {
			f.t.Errorf("POST /gists: not a secret gist")
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": "bad"})
			return
		}
		f.next++
		id := fmt.Sprintf("%032x", f.next)
		g := &fakeGist{desc: body.Description, updated: time.Now(), files: map[string]string{}}
		for name, fl := range body.Files {
			g.files[name] = fl.Content
		}
		f.gists[id] = g
		f.order = append(f.order, id)
		writeJSON(w, http.StatusCreated, f.gistJSON(id, true))
	case strings.HasPrefix(r.URL.Path, "/gists/"):
		id := strings.TrimPrefix(r.URL.Path, "/gists/")
		g := f.gists[id]
		if g == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, f.gistJSON(id, true))
		case http.MethodPatch:
			var body struct {
				Files map[string]*struct {
					Content string `json:"content"`
				} `json:"files"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": "bad"})
				return
			}
			for name, fl := range body.Files {
				if fl == nil {
					f.nulls = append(f.nulls, name)
					delete(g.files, name)
				} else {
					g.files[name] = fl.Content
				}
			}
			g.updated = time.Now()
			writeJSON(w, http.StatusOK, f.gistJSON(id, true))
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	default:
		http.NotFound(w, r)
	}
}

func name(t time.Time) string { return backup.Name(t, "ab12cd34") }

var t0 = time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)

// TestFindsGistByDescription: among many gists over pages, the newest
// with the description is chosen, over one only holding a backup file.
func TestFindsGistByDescription(t *testing.T) {
	f := newFake(t)
	f.add(Description, t0, map[string]string{name(t0): "old"})
	f.add("", t0.Add(3*time.Hour), map[string]string{name(t0): "stray"})
	for i := range 140 {
		f.add("notes "+strconv.Itoa(i), t0, map[string]string{"a.txt": "x"})
	}
	want := f.add(Description, t0.Add(time.Hour), map[string]string{name(t0.Add(time.Hour)): "new", "readme.md": "x"})
	g := f.client("")
	var bound []string
	g.Bound = func(id string) { bound = append(bound, id) }
	objs, err := g.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if g.ID != want || len(bound) != 1 || bound[0] != want {
		t.Fatalf("found %q, bound %v; want %q", g.ID, bound, want)
	}
	if len(objs) != 1 || objs[0].Name != name(t0.Add(time.Hour)) || objs[0].Device != "ab12cd34" || objs[0].Size != 3 {
		t.Fatalf("listed %+v", objs)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	pages := 0
	for _, r := range f.requests {
		if r == "GET /gists" {
			pages++
		}
	}
	if pages != 2 {
		t.Fatalf("read %d pages, want 2: %v", pages, f.requests)
	}
}

// TestFallbackGist: with no described gist, one holding a backup's file is used.
func TestFallbackGist(t *testing.T) {
	f := newFake(t)
	f.add("other", t0, map[string]string{"a.txt": "x"})
	want := f.add("renamed", t0, map[string]string{name(t0): "b"})
	g := f.client("")
	if err := g.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if g.ID != want {
		t.Fatalf("ID %q, want %q", g.ID, want)
	}
}

// TestCreateOnFirstPut: with no gist, List is empty, the first Put makes a
// secret gist and binds it, later ones add files beside it.
func TestCreateOnFirstPut(t *testing.T) {
	f := newFake(t)
	g := f.client("")
	var bound string
	g.Bound = func(id string) { bound = id }
	ctx := context.Background()
	if err := g.Check(ctx); err != nil {
		t.Fatal("check with no gist:", err)
	}
	if objs, err := g.List(ctx); err != nil || len(objs) != 0 {
		t.Fatalf("empty list: %v, %v", objs, err)
	}
	if err := g.Put(ctx, name(t0), []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	ok := bound != "" && g.ID == bound && f.gists[bound] != nil && f.gists[bound].desc == Description
	f.mu.Unlock()
	if !ok {
		t.Fatalf("bound %q, ID %q", bound, g.ID)
	}
	if err := g.Put(ctx, name(t0.Add(time.Hour)), []byte(`{"b":2}`)); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.gists[bound].files["notes.txt"] = "not a backup"
	n := len(f.gists)
	f.mu.Unlock()
	if n != 1 {
		t.Fatalf("%d gists made", n)
	}
	objs, err := g.List(ctx)
	if err != nil || len(objs) != 2 || objs[0].Name != name(t0.Add(time.Hour)) || objs[1].Name != name(t0) {
		t.Fatalf("listed %+v, %v", objs, err)
	}
	b, err := g.Get(ctx, name(t0))
	if err != nil || string(b) != `{"a":1}` {
		t.Fatalf("got %q, %v", b, err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !strings.Contains(strings.Join(f.requests, ","), "PATCH /gists/"+bound) {
		t.Fatalf("second put did not patch: %v", f.requests)
	}
}

// TestGetTruncated: a file the API cut short is read from its raw
// address on the API's host; on another plain-http host it is refused.
func TestGetTruncated(t *testing.T) {
	f := newFake(t)
	full := `{"body":"` + strings.Repeat("x", 100) + `"}`
	id := f.add(Description, t0, map[string]string{name(t0): full})
	f.mu.Lock()
	f.truncate = 10
	f.mu.Unlock()
	g := f.client(id)
	b, err := g.Get(context.Background(), name(t0))
	if err != nil || string(b) != full {
		t.Fatalf("got %q, %v", b, err)
	}
	f.mu.Lock()
	raws := len(f.rawAuth)
	f.mu.Unlock()
	if raws != 1 {
		t.Fatalf("%d raw reads", raws)
	}

	var foreignHit atomic.Bool
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { foreignHit.Store(true) }))
	defer foreign.Close()
	f.mu.Lock()
	f.rawBase = foreign.URL
	f.mu.Unlock()
	if _, err := g.Get(context.Background(), name(t0)); err == nil || foreignHit.Load() {
		t.Fatalf("raw address on a foreign host: err %v, reached %v", err, foreignHit.Load())
	}
}

// TestDelete: a backup is removed by setting its file to null.
func TestDelete(t *testing.T) {
	f := newFake(t)
	id := f.add(Description, t0, map[string]string{name(t0): "a", name(t0.Add(time.Hour)): "b"})
	g := f.client(id)
	ctx := context.Background()
	if err := g.Delete(ctx, name(t0)); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	nulls := append([]string(nil), f.nulls...)
	f.mu.Unlock()
	if len(nulls) != 1 || nulls[0] != name(t0) {
		t.Fatalf("nulls %v", nulls)
	}
	objs, _ := g.List(ctx)
	if len(objs) != 1 || objs[0].Name != name(t0.Add(time.Hour)) {
		t.Fatalf("left %+v", objs)
	}
	if err := g.Delete(ctx, "notes.txt"); !errors.Is(err, ErrName) {
		t.Fatalf("delete of another file: %v", err)
	}
}

func TestErrors(t *testing.T) {
	f := newFake(t)
	id := f.add(Description, t0, map[string]string{name(t0): "a"})
	ctx := context.Background()

	wrong := &Gist{Token: "ghp_wrongTOKEN456", ID: id, API: f.srv.URL}
	_, err := wrong.List(ctx)
	if !errors.Is(err, backup.ErrAuth) || strings.Contains(err.Error(), "TOKEN") {
		t.Fatalf("wrong token: %v", err)
	}
	if err := (&Gist{Token: "ghp_wrongTOKEN456", API: f.srv.URL}).Check(ctx); !errors.Is(err, backup.ErrAuth) {
		t.Fatalf("check with a wrong token: %v", err)
	}
	if err := f.client(fmt.Sprintf("%032x", 999)).Check(ctx); !errors.Is(err, backup.ErrNotFound) {
		t.Fatalf("unknown gist: %v", err)
	}
	if _, err := f.client("../users").List(ctx); !errors.Is(err, backup.ErrNotFound) {
		t.Fatalf("bad ID: %v", err)
	}
	if _, err := f.client(id).Get(ctx, name(t0.Add(time.Hour))); !errors.Is(err, backup.ErrNotFound) {
		t.Fatalf("missing file: %v", err)
	}

	f.mu.Lock()
	before := len(f.requests)
	f.mu.Unlock()
	if err := f.client(id).Put(ctx, name(t0), []byte{0xff, 0xfe}); !errors.Is(err, ErrText) {
		t.Fatalf("binary data: %v", err)
	}
	if err := f.client(id).Put(ctx, "../x"+backup.Ext, []byte("a")); !errors.Is(err, ErrName) {
		t.Fatalf("bad name: %v", err)
	}
	for _, tk := range []string{"", "ghp_a b", "ghp_a\r\nX-Evil: 1"} {
		g := &Gist{Token: tk, ID: id, API: f.srv.URL}
		if _, err := g.List(ctx); !errors.Is(err, ErrToken) {
			t.Fatalf("token %q: %v", tk, err)
		}
		if err := g.Put(ctx, name(t0), []byte("a")); !errors.Is(err, ErrToken) {
			t.Fatalf("put with token %q: %v", tk, err)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) != before {
		t.Fatalf("refused calls reached the API: %v", f.requests[before:])
	}
}
