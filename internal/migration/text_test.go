package migration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTextImportFields(t *testing.T) {
	text := "\xef\xbb\xbf# hosts\r\n" +
		"host=192.168.31.22 port=22 user=root authType=password title=31.22\r\n" +
		"——————\n" +
		"\n" +
		"ip=192.168.78.55 PORT=2222 username=admin password=11223344 name=test\n" +
		"HOST=db.example.com Label='生产 数据库' password=\"a \\\"b\\\\ c\" zone=cn tags=x\n" +
		"host=edge password=p\\a\"ss=word title=\"边缘\"节点\n" +
		"host=bad port=http title=broken\n" +
		"user=root title=missing\n" +
		"host=open title=\"no end password=secret\n"
	r, err := parseData(Text, []byte(text), "")
	if err != nil {
		t.Fatal(err)
	}
	for i := range r.Entries {
		validateEntry(&r.Entries[i])
	}
	if len(r.Entries) != 7 || len(r.Issues) != 0 {
		t.Fatalf("entries %+v issues %v", r.Entries, r.Issues)
	}
	want := []struct {
		name, address, user, password string
		port                          int
	}{
		{"31.22", "192.168.31.22", "root", "", 22},
		{"test", "192.168.78.55", "admin", "11223344", 2222},
		{"生产 数据库", "db.example.com", "", `a "b\ c`, 22},
		{"边缘节点", "edge", "", `p\a"ss=word`, 22},
		{"broken", "bad", "", "", -1},
		{"missing", "", "root", "", 22},
		{"no end password=secret", "open", "", "", 22},
	}
	for i, w := range want {
		e := r.Entries[i]
		if e.Name != w.name || e.Address != w.address || e.User != w.user || string(e.Password) != w.password || e.Port != w.port {
			t.Fatalf("entry %d = %+v (password %q), want %+v", i, e, e.Password, w)
		}
	}
	if len(r.Entries[0].Issues) != 0 || len(r.Entries[1].Issues) != 0 {
		t.Fatalf("authType or aliases reported: %v %v", r.Entries[0].Issues, r.Entries[1].Issues)
	}
	if issues := r.Entries[2].Issues; len(issues) != 2 || !hasIssue(issues, "unsupported-field", "zone") || !hasIssue(issues, "unsupported-field", "tags") {
		t.Fatalf("unknown keys %v", issues)
	}
	if !hasIssue(r.Entries[4].Issues, "invalid-entry", "port") || !hasIssue(r.Entries[5].Issues, "invalid-entry", "address") {
		t.Fatalf("invalid fields %v %v", r.Entries[4].Issues, r.Entries[5].Issues)
	}
	if !hasIssue(r.Entries[6].Issues, "unclosed-quote", "title") {
		t.Fatalf("unclosed quote %+v", r.Entries[6])
	}
}

func TestTextImportBounds(t *testing.T) {
	r, err := ParseText("host=a.example password=secret\n")
	if err != nil || len(r.Entries) != 1 || r.Entries[0].Name != "a.example" || r.Entries[0].Port != 22 || string(r.Entries[0].Password) != "secret" {
		t.Fatalf("parse %+v %v", r, err)
	}
	for _, text := range []string{"", "\n# only a comment\n——\n"} {
		if _, err := ParseText(text); err != ErrNoEntries {
			t.Fatalf("%q: %v", text, err)
		}
	}
	if _, err := ParseText(strings.Repeat("host=a\n", MaxEntries+1)); err != ErrTooLarge {
		t.Fatalf("entry bound: %v", err)
	}
	if _, err := ParseText(strings.Repeat(" ", int(MaxBytes)+1)); err != ErrTooLarge {
		t.Fatalf("size bound: %v", err)
	}
	path := filepath.Join(t.TempDir(), "hosts.txt")
	if err := os.WriteFile(path, []byte("host=b.example user=ops title=b\n"), 0600); err != nil {
		t.Fatal(err)
	}
	r, err = Read(Text, []string{path})
	if err != nil || len(r.Entries) != 1 || r.Entries[0].Name != "b" || r.Entries[0].User != "ops" {
		t.Fatalf("read %+v %v", r, err)
	}
}
