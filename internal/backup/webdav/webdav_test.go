package webdav

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/backup"
	"golang.org/x/net/webdav"
)

// server is a WebDAV server in memory that takes user "me", password "pw".
func server(t *testing.T) (*httptest.Server, webdav.FileSystem) {
	t.Helper()
	fs := webdav.NewMemFS()
	h := &webdav.Handler{FileSystem: fs, LockSystem: webdav.NewMemLS()}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "me" || p != "pw" {
			w.Header().Set("WWW-Authenticate", `Basic realm="dav"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, fs
}

func TestFolderRoundTrip(t *testing.T) {
	srv, fs := server(t)
	ctx := context.Background()
	f := &Folder{URL: srv.URL + "/", Dir: "SSHYard/backups", User: "me", Password: "pw"}
	if err := f.Check(ctx); err != nil {
		t.Fatal("check before the folder exists:", err)
	}
	if objs, err := f.List(ctx); err != nil || len(objs) != 0 {
		t.Fatalf("list of a missing folder: %v, %v", objs, err)
	}
	t0 := time.Date(2026, 11, 4, 10, 15, 0, 0, time.UTC)
	older, newer := backup.Name(t0, "ab12cd34"), backup.Name(t0.Add(time.Hour), "ab12cd34")
	for _, n := range []string{older, newer} {
		if err := f.Put(ctx, n, []byte("sealed "+n)); err != nil {
			t.Fatal(err)
		}
	}
	// Files that are not backups are not listed.
	if fl, err := fs.OpenFile(ctx, "/SSHYard/backups/notes.txt", os.O_RDWR|os.O_CREATE, 0o600); err == nil {
		fl.Write([]byte("x"))
		fl.Close()
	} else {
		t.Fatal(err)
	}
	fs.Mkdir(ctx, "/SSHYard/backups/"+backup.Name(t0, "dir"), 0o700)
	objs, err := f.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 2 || objs[0].Name != newer || objs[1].Name != older || objs[0].Device != "ab12cd34" || objs[0].Size != int64(len("sealed "+newer)) {
		t.Fatalf("listed %+v", objs)
	}
	b, err := f.Get(ctx, older)
	if err != nil || string(b) != "sealed "+older {
		t.Fatalf("got %q, %v", b, err)
	}
	if err := f.Delete(ctx, older); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Get(ctx, older); !errors.Is(err, backup.ErrNotFound) {
		t.Fatalf("deleted file: %v", err)
	}
	if err := f.Put(ctx, "../escape", []byte("x")); err == nil {
		t.Fatal("put a name that is not a backup's")
	}
}

func TestFolderWrongPassword(t *testing.T) {
	srv, _ := server(t)
	f := &Folder{URL: srv.URL, User: "me", Password: "nope"}
	err := f.Check(context.Background())
	if !errors.Is(err, backup.ErrAuth) {
		t.Fatalf("wrong password: %v", err)
	}
	if strings.Contains(err.Error(), "nope") {
		t.Fatalf("the password is in %q", err)
	}
}

func TestFolderURL(t *testing.T) {
	for _, u := range []string{"", "ftp://h/", "https://me:pw@h/", "https:///x", "https://h/?a=1", "https://h/#x"} {
		f := &Folder{URL: u}
		if err := f.Check(context.Background()); !errors.Is(err, ErrURL) {
			t.Errorf("%q: %v", u, err)
		}
	}
}

// TestFolderRedirectStays: a redirect to another host is not followed,
// so the credentials go nowhere else.
func TestFolderRedirectStays(t *testing.T) {
	var leaked bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = true
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer other.Close()
	srv := httptest.NewServer(http.RedirectHandler(other.URL+"/", http.StatusTemporaryRedirect))
	defer srv.Close()
	f := &Folder{URL: srv.URL, User: "me", Password: "pw"}
	if err := f.Check(context.Background()); err == nil || leaked {
		t.Fatalf("followed a redirect to another host: %v, leaked %v", err, leaked)
	}
}

func TestFolderCanceled(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-block }))
	defer srv.Close()
	defer close(block)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- (&Folder{URL: srv.URL}).Put(ctx, backup.Name(time.Now(), "ab"), bytes.Repeat([]byte("x"), 10))
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled put: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a canceled put kept waiting")
	}
}
