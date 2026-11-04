package backup

import (
	"archive/zip"
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNames(t *testing.T) {
	at := time.Date(2026, 11, 4, 18, 15, 7, 0, time.FixedZone("CST", 8*3600))
	n := Name(at, "ab12cd34")
	if n != "SSHYard-20261104T101507Z-ab12cd34.sshyard-backup" {
		t.Fatalf("name %q", n)
	}
	got, dev, ok := Parse(n)
	if !ok || !got.Equal(at) || dev != "ab12cd34" {
		t.Fatalf("parsed %v %q %v", got, dev, ok)
	}
	for _, bad := range []string{"", "notes.txt", "SSHYard-20261104T101507Z-ab12cd34.json", "SSHYard-20261104T101507Z-.sshyard-backup",
		"SSHYard-20261104T101507Z-AB.sshyard-backup", "SSHYard-2026-11-04-ab.sshyard-backup", "SSHYard-20261104T101507Z-a/b.sshyard-backup"} {
		if _, _, ok := Parse(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestPrune(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var objs []Object
	for i := range 5 {
		o, _ := ObjectOf(Name(t0.Add(time.Duration(i)*time.Hour), "mine"), 1)
		objs = append(objs, o)
	}
	other, _ := ObjectOf(Name(t0, "other"), 1)
	objs = append(objs, other)
	gone := Prune(objs, "mine", 2)
	if len(gone) != 3 || gone[0].Time != t0.Add(2*time.Hour) || gone[2].Time != t0 {
		t.Fatalf("pruned %+v", gone)
	}
	for _, o := range gone {
		if o.Device != "mine" {
			t.Fatalf("pruned another device's %s", o.Name)
		}
	}
	if Prune(objs, "mine", 0) != nil || Prune(objs, "mine", 5) != nil {
		t.Fatal("pruned with keep 0 or enough room")
	}
}

func TestArchiveRoundTrip(t *testing.T) {
	m := Manifest{App: "0.0.6", Created: time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC), Device: "ab12cd34", Secrets: true}
	files := map[string][]byte{"assets.json": bytes.Repeat([]byte(`{"hosts":[]}`), 1000), "known_hosts": []byte("h ssh-ed25519 AAAA\n")}
	b, err := Pack(m, files)
	if err != nil {
		t.Fatal(err)
	}
	if !IsArchive(b) || IsArchive([]byte(`{"assets":{}}`)) {
		t.Fatal("IsArchive")
	}
	if len(b) > len(files["assets.json"])/4 {
		t.Fatalf("not compressed: %d bytes", len(b))
	}
	got, out, err := Unpack(b)
	if err != nil {
		t.Fatal(err)
	}
	if got.Format != Format || got.Version != Version || got.Device != "ab12cd34" || !got.Secrets || !got.Created.Equal(m.Created) {
		t.Fatalf("manifest %+v", got)
	}
	if len(out) != 2 || !bytes.Equal(out["assets.json"], files["assets.json"]) || string(out["known_hosts"]) != "h ssh-ed25519 AAAA\n" {
		t.Fatalf("files %v", out)
	}
	if _, err := Pack(m, map[string][]byte{"../x": nil}); err == nil {
		t.Fatal("packed a path")
	}
}

// zipOf is a zip of the named files, in order.
func zipOf(t *testing.T, files ...[2]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		w, err := zw.Create(f[0])
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(f[1]))
	}
	zw.Close()
	return buf.Bytes()
}

func TestUnpackRefuses(t *testing.T) {
	manifest := func(v int) [2]string {
		return [2]string{manifestName, `{"format":"sshyard-backup","version":` + string(rune('0'+v)) + `}`}
	}
	for name, b := range map[string][]byte{
		"not a zip":   []byte("PK\x03\x04garbage"),
		"no manifest": zipOf(t, [2]string{"assets.json", "{}"}),
		"path":        zipOf(t, manifest(2), [2]string{"../assets.json", "{}"}),
		"folder":      zipOf(t, manifest(2), [2]string{"a/assets.json", "{}"}),
		"twice":       zipOf(t, manifest(2), [2]string{"assets.json", "{}"}, [2]string{"assets.json", "{}"}),
		"two":         zipOf(t, manifest(2), manifest(2)),
		"version 1":   zipOf(t, manifest(1)),
		"other":       zipOf(t, [2]string{manifestName, `{"format":"other","version":2}`}),
	} {
		if _, _, err := Unpack(b); !errors.Is(err, ErrArchive) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, _, err := Unpack(zipOf(t, manifest(3))); !errors.Is(err, ErrVersion) {
		t.Errorf("newer: %v", err)
	}
}

// TestUnpackBounded: what an archive unpacks to is bounded, whatever its
// headers say.
func TestUnpackBounded(t *testing.T) {
	big := strings.Repeat("0", MaxSize/2+1)
	b := zipOf(t, [2]string{manifestName, `{"format":"sshyard-backup","version":2}`}, [2]string{"a", big}, [2]string{"b", big})
	if _, _, err := Unpack(b); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("unpacked past the bound: %v", err)
	}
}

func TestStatusError(t *testing.T) {
	for status, want := range map[int]error{401: ErrAuth, 403: ErrAuth, 404: ErrNotFound} {
		if err := error(&StatusError{Status: status}); !errors.Is(err, want) {
			t.Errorf("%d is not %v", status, want)
		}
	}
	if errors.Is(&StatusError{Status: 500}, ErrAuth) || errors.Is(&StatusError{Status: 500}, ErrNotFound) {
		t.Error("500 matched")
	}
	if s := (&StatusError{Status: 403, Code: "AccessDenied", Message: "no"}).Error(); s != "HTTP 403 AccessDenied: no" {
		t.Errorf("text %q", s)
	}
}

func TestClientStaysOnHost(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("followed to another host") }))
	defer other.Close()
	srv := httptest.NewServer(http.RedirectHandler(other.URL, http.StatusFound))
	defer srv.Close()
	resp, err := Client(nil).Get(srv.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("redirect to another host followed")
	}
}

func TestReadAll(t *testing.T) {
	if _, err := ReadAll(strings.NewReader(strings.Repeat("x", MaxSize+1))); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("read past MaxSize: %v", err)
	}
}
