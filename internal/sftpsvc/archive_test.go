package sftpsvc

import (
	"archive/tar"
	"archive/zip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestArchiveNames(t *testing.T) {
	for _, tc := range []struct {
		name         string
		local, other bool
		base         string
	}{
		{"a.zip", true, true, "a"},
		{"a.tar.gz", true, true, "a"},
		{"A.TGZ", true, true, "A"},
		{"a.tar.bz2", true, true, "a"},
		{"a.tar.xz", false, true, "a"},
		{"a.tar", true, true, "a"},
		{"a.txt", false, false, "a.txt"},
		{".zip", false, false, ".zip"},
	} {
		if got := IsArchive(tc.name, false); got != tc.local {
			t.Errorf("IsArchive(%q, local) = %v", tc.name, got)
		}
		if got := IsArchive(tc.name, true); got != tc.other {
			t.Errorf("IsArchive(%q, remote) = %v", tc.name, got)
		}
		if got := ArchiveBase(tc.name); got != tc.base {
			t.Errorf("ArchiveBase(%q) = %q", tc.name, got)
		}
	}
}

func TestArchiveCommands(t *testing.T) {
	got := CompressCommand("/srv/it's", []string{"a b", "-rf"}, "/srv/it's/x.tar.gz", TarGz)
	want := `cd '/srv/it'\''s' && tar -czf '/srv/it'\''s/x.tar.gz' './a b' './-rf'`
	if got != want {
		t.Errorf("tar:\n got %s\nwant %s", got, want)
	}
	got = CompressCommand("/srv", []string{"d"}, "/srv/d.zip", Zip)
	if want := `cd '/srv' && zip -q -r -y '/srv/d.zip' './d'`; got != want {
		t.Errorf("zip:\n got %s\nwant %s", got, want)
	}
	for archive, want := range map[string]string{
		"/x/a.zip":     `unzip -q -o '/x/a.zip' -d '/x/a'`,
		"/x/a.tar.gz":  `tar -xzf '/x/a.tar.gz' -C '/x/a'`,
		"/x/a.tar.xz":  `tar -xJf '/x/a.tar.xz' -C '/x/a'`,
		"/x/a.tar":     `tar -xf '/x/a.tar' -C '/x/a'`,
		"/x/a.tar.bz2": `tar -xjf '/x/a.tar.bz2' -C '/x/a'`,
	} {
		got, err := ExtractCommand(archive, "/x/a")
		if err != nil || got != want {
			t.Errorf("%s: %s %v, want %s", archive, got, err, want)
		}
	}
	if _, err := ExtractCommand("/x/a.txt", "/x/a"); !errors.Is(err, ErrNotArchive) {
		t.Errorf("not an archive: %v", err)
	}
}

func TestCompressExtractLocal(t *testing.T) {
	for _, f := range []ArchiveFormat{Zip, TarGz} {
		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, "d", "a.txt"), "a")
		mustWrite(t, filepath.Join(dir, "d", "sub", "b.txt"), "bb")
		mustWrite(t, filepath.Join(dir, "c.txt"), "ccc")
		dst := filepath.Join(dir, "out"+f.Ext())
		if err := CompressLocal(context.Background(), dir, []string{"d", "c.txt"}, dst, f); err != nil {
			t.Fatal(err)
		}
		into := filepath.Join(dir, "x")
		if err := os.Mkdir(into, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := ExtractLocal(context.Background(), dst, into); err != nil {
			t.Fatal(err)
		}
		for rel, want := range map[string]string{"d/a.txt": "a", "d/sub/b.txt": "bb", "c.txt": "ccc"} {
			if b, err := os.ReadFile(filepath.Join(into, rel)); err != nil || string(b) != want {
				t.Errorf("%s %s: %q %v", f.Ext(), rel, b, err)
			}
		}
		// The archive is not made over a file already there.
		if err := CompressLocal(context.Background(), dir, []string{"c.txt"}, dst, f); err == nil {
			t.Errorf("%s: made over a file", f.Ext())
		}
	}
}

func TestExtractLocalStaysInside(t *testing.T) {
	dir := t.TempDir()
	zp := filepath.Join(dir, "evil.zip")
	out, err := os.Create(zp)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(out)
	w, _ := zw.Create("../escaped.txt")
	w.Write([]byte("x"))
	zw.Close()
	out.Close()
	into := filepath.Join(dir, "in")
	os.Mkdir(into, 0o755)
	if err := ExtractLocal(context.Background(), zp, into); err == nil {
		t.Error("an entry outside was extracted")
	}
	if _, err := os.Stat(filepath.Join(dir, "escaped.txt")); err == nil {
		t.Error("escaped.txt written outside")
	}

	// A tar's link is left out, and a file through it is not written.
	tp := filepath.Join(dir, "evil.tar")
	out, err = os.Create(tp)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(out)
	tw.WriteHeader(&tar.Header{Name: "up", Typeflag: tar.TypeSymlink, Linkname: ".."})
	tw.WriteHeader(&tar.Header{Name: "up/escaped2.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1})
	tw.Write([]byte("x"))
	tw.Close()
	out.Close()
	into2 := filepath.Join(dir, "in2")
	os.Mkdir(into2, 0o755)
	ExtractLocal(context.Background(), tp, into2)
	if _, err := os.Lstat(filepath.Join(into2, "up")); err == nil {
		if fi, _ := os.Lstat(filepath.Join(into2, "up")); fi.Mode()&os.ModeSymlink != 0 {
			t.Error("link extracted")
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "escaped2.txt")); err == nil {
		t.Error("escaped2.txt written outside")
	}
}

func TestLocalDirSize(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a"), "12345")
	mustWrite(t, filepath.Join(dir, "s", "b"), "123")
	size, files, err := LocalDirSize(context.Background(), dir)
	if err != nil || size != 8 || files != 2 {
		t.Errorf("size %d files %d err %v", size, files, err)
	}
}

func mustWrite(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}
